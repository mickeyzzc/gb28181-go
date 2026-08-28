package device

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// testRecordingIndex is a playback-capable RecordingIndex backed by a
// temp-dir recording (implements PlaybackIndex).
type testRecordingIndex struct {
	root     string
	segments []SegmentMeta
}

func (t *testRecordingIndex) Lookup(startMs, endMs int64) []SegmentMeta {
	var out []SegmentMeta
	for _, si := range t.segments {
		if si.EndMS >= startMs && si.StartMS <= endMs {
			out = append(out, si)
		}
	}
	return out
}

func (t *testRecordingIndex) Root() string { return t.root }

// synthFrame describes one frame of a synthetic recording.
type synthFrame struct {
	offset time.Duration
	key    bool
}

// writeSyntheticRecording writes a synthetic recording in the reference
// segment format (bare Annex-B + .ts.jsonl sidecar) into dir and returns
// the index snapshot.
func writeSyntheticRecording(t *testing.T, dir string, frames []synthFrame) []SegmentMeta {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	base := time.Now().Add(-time.Hour).Truncate(time.Second)
	var annexB, sidecar bytes.Buffer
	keyframes := 0
	for _, f := range frames {
		var types []byte
		if f.key {
			types = []byte{7, 8, 5} // SPS, PPS, IDR
			keyframes++
		} else {
			types = []byte{1}
		}
		for _, ty := range types {
			annexB.Write([]byte{0x00, 0x00, 0x00, 0x01})
			annexB.Write([]byte{ty, 0x01, 0x02, 0x03})
		}
		fmt.Fprintf(&sidecar, "{\"pts_ms\":%d}\n", f.offset.Milliseconds())
	}

	file := filepath.Join(dir, "000001.h264")
	if err := os.WriteFile(file, annexB.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file+".ts.jsonl", sidecar.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}

	last := frames[len(frames)-1].offset
	return []SegmentMeta{{
		File:      "000001.h264",
		StartMS:   base.UnixMilli(),
		EndMS:     base.Add(last).UnixMilli(),
		Size:      int64(annexB.Len()),
		Frames:    len(frames),
		Keyframes: keyframes,
	}}
}

// startPlaybackServer starts a test-mode gb28181 server with the given
// recording index (nil = none) and returns the server and SIP port.
func startPlaybackServer(t *testing.T, idx RecordingIndex) (*Server, int, context.CancelFunc) {
	t.Helper()
	hub := NewFrameHub()
	cfg := Config{
		DeviceID:              "34020000001320000001",
		ChannelID:             "34020000001320000001",
		SIPDomain:             "3402000000",
		Password:              "12345678",
		LocalSIPPort:          0,
		PlatformSIPAddress:    "127.0.0.1",
		PlatformSIPPort:       15068,
		RegisterIntervalSecs:  60,
		HeartbeatIntervalSecs: 60,
		HeartbeatTimeoutCount: 3,
	}
	server := New(cfg, DeviceInfo{}, hub)
	server.SetTestMode()
	if idx != nil {
		server.SetRecordingIndex(idx)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	go func() { _ = server.Start(ctx) }()
	time.Sleep(100 * time.Millisecond)
	sipPort := waitSIPPort(t, server)
	return server, sipPort, cancel
}

// buildPlaybackInvite builds an INVITE with the given session type and
// t= range (unix seconds), pointing media at mediaPort.
func buildPlaybackInvite(callID, sessionType string, startSec, endSec int64, mediaPort int) SipMessage {
	sdp := fmt.Sprintf("v=0\no=- 0 0 IN IP4 127.0.0.1\ns=%s\nc=IN IP4 127.0.0.1\nt=%d %d\nm=video %d RTP/AVP 96\na=rtpmap:96 PS/90000\na=recvonly\ny=0100000001", sessionType, startSec, endSec, mediaPort)
	inv := buildInvite(callID,
		"<sip:34020000012000000001@3402000000>;tag=pb001",
		"<sip:34020000001320000001@3402000000>",
		"<sip:34020000012000000001@127.0.0.1:5060>")
	inv.Body = sdp
	return inv
}

// TestParseSDP_SessionType verifies s= extraction: Play/Playback/Download,
// case-insensitive, missing or unknown values defaulting to "Play".
func TestParseSDP_SessionType(t *testing.T) {
	base := "v=0\no=- 0 0 IN IP4 192.168.1.100\nc=IN IP4 192.168.1.100\nt=0 0\nm=video 60000 RTP/AVP 96\na=rtpmap:96 PS/90000\na=recvonly\ny=0100000001"
	cases := []struct {
		name string
		s    string // s= line content, "" = omit
		want string
	}{
		{"play", "Play", "Play"},
		{"playback", "Playback", "Playback"},
		{"download", "Download", "Download"},
		{"lowercase", "playback", "Playback"},
		{"mixed case", "PLAYBACK", "Playback"},
		{"missing", "", "Play"},
		{"unknown", "SomethingElse", "Play"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := base
			if tc.s != "" {
				body = "s=" + tc.s + "\n" + body
			}
			_, _, got, err := parseSDP(body)
			if err != nil {
				t.Fatalf("parseSDP: %v", err)
			}
			if got != tc.want {
				t.Errorf("session type = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestParseSDPTimeRange verifies t= extraction: unix seconds to ms range,
// t=0 0 and missing/malformed lines meaning "all".
func TestParseSDPTimeRange(t *testing.T) {
	cases := []struct {
		name        string
		body        string
		wantStartMs int64
		wantEndMs   int64
	}{
		{"explicit", "v=0\ns=Playback\nt=1750000000 1750003600\n", 1750000000000, 1750003600000},
		{"zero zero means all", "v=0\ns=Playback\nt=0 0\n", 0, int64(^uint64(0) >> 1)},
		{"missing means all", "v=0\ns=Playback\n", 0, int64(^uint64(0) >> 1)},
		{"malformed means all", "v=0\ns=Playback\nt=abc def\n", 0, int64(^uint64(0) >> 1)},
		{"single field means all", "v=0\ns=Playback\nt=1750000000\n", 0, int64(^uint64(0) >> 1)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			start, end := parseSDPTimeRange(tc.body)
			if start != tc.wantStartMs || end != tc.wantEndMs {
				t.Errorf("parseSDPTimeRange = (%d, %d), want (%d, %d)", start, end, tc.wantStartMs, tc.wantEndMs)
			}
		})
	}
}

// TestBuildDeviceSDP_EchoesSessionType verifies the SDP answer echoes the
// session type in the s= line and that the UDP s=Play output is
// byte-identical to the pre-playback golden format.
func TestBuildDeviceSDP_EchoesSessionType(t *testing.T) {
	const (
		deviceID = "34020000001320000001"
		localIP  = "192.168.1.50"
		port     = 40000
		ssrc     = uint32(100000001)
	)
	udpPlay := buildDeviceSDP(deviceID, localIP, port, ssrc, "udp", "Play")
	wantUDP := `v=0
o=34020000001320000001 0 0 IN IP4 192.168.1.50
s=Play
c=IN IP4 192.168.1.50
t=0 0
m=video 40000 RTP/AVP 96
a=sendonly
a=rtpmap:96 PS/90000
y=100000001`
	if udpPlay != wantUDP {
		t.Errorf("UDP s=Play SDP not byte-identical to golden:\ngot:\n%s\nwant:\n%s", udpPlay, wantUDP)
	}

	for _, st := range []string{"Playback", "Download"} {
		got := buildDeviceSDP(deviceID, localIP, port, ssrc, "udp", st)
		if !containsSubstring(got, "s="+st) {
			t.Errorf("UDP SDP missing s=%s:\n%s", st, got)
		}
		if strings.Replace(got, "s="+st, "s=Play", 1) != udpPlay {
			t.Errorf("UDP SDP for %s differs beyond the s= line:\n%s", st, got)
		}
	}

	tcpPlay := buildDeviceSDP(deviceID, localIP, port, ssrc, "tcp", "Play")
	if !containsSubstring(tcpPlay, "s=Play") || !containsSubstring(tcpPlay, "TCP/RTP/AVP") {
		t.Errorf("TCP s=Play SDP malformed:\n%s", tcpPlay)
	}
	for _, st := range []string{"Playback", "Download"} {
		got := buildDeviceSDP(deviceID, localIP, port, ssrc, "tcp", st)
		if !containsSubstring(got, "s="+st) {
			t.Errorf("TCP SDP missing s=%s:\n%s", st, got)
		}
		if strings.Replace(got, "s="+st, "s=Play", 1) != tcpPlay {
			t.Errorf("TCP SDP for %s differs beyond the s= line:\n%s", st, got)
		}
	}
}

// TestServer_PlaybackStreamsRecordingWithPacing verifies an end-to-end
// Playback INVITE: 200 OK echoes s=Playback, RTP flows to the SDP media
// address, the first frame is a keyframe (PSM present), and frames are
// paced at the recorded ~100ms interval.
func TestServer_PlaybackStreamsRecordingWithPacing(t *testing.T) {
	dir := t.TempDir()
	frames := []synthFrame{{0, true}}
	for i := 1; i < 10; i++ {
		frames = append(frames, synthFrame{time.Duration(i) * 100 * time.Millisecond, false})
	}
	segs := writeSyntheticRecording(t, dir, frames)
	idx := &testRecordingIndex{root: dir, segments: segs}

	_, sipPort, cancel := startPlaybackServer(t, idx)
	defer cancel()

	mediaSock, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatalf("bind media socket: %v", err)
	}
	defer mediaSock.Close()
	mediaPort := mediaSock.LocalAddr().(*net.UDPAddr).Port

	invite := buildPlaybackInvite("test-call-pb@example.com", "Playback", segs[0].StartMS/1000, segs[0].EndMS/1000+1, mediaPort)
	clientConn, err := net.DialUDP("udp", nil, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: sipPort})
	if err != nil {
		t.Fatalf("dial server: %v", err)
	}
	defer clientConn.Close()

	if _, err := clientConn.Write(invite.Serialize()); err != nil {
		t.Fatalf("send INVITE: %v", err)
	}

	respBuf := make([]byte, 4096)
	clientConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, _, err := clientConn.ReadFromUDP(respBuf)
	if err != nil {
		t.Fatalf("read 200 OK: %v", err)
	}
	resp, err := Parse(respBuf[:n])
	if err != nil {
		t.Fatalf("parse 200 OK: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
	}
	if !containsSubstring(resp.Body, "s=Playback") {
		t.Fatalf("SDP answer does not echo s=Playback:\n%s", resp.Body)
	}

	// One RTP packet per frame; record arrival times.
	var arrivals []time.Time
	var firstPayload []byte
	mediaSock.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, 2048)
	for len(arrivals) < len(frames) {
		n, _, err := mediaSock.ReadFromUDP(buf)
		if err != nil {
			t.Fatalf("read RTP frame %d: %v", len(arrivals), err)
		}
		if n < 12 || buf[0]>>6 != 2 {
			t.Fatalf("packet %d is not RTP v2 (len=%d)", len(arrivals), n)
		}
		if len(arrivals) == 0 {
			firstPayload = append([]byte(nil), buf[:n]...)
		}
		arrivals = append(arrivals, time.Now())
	}

	// First frame must be a keyframe: PSM (0x000001BB) is only emitted on
	// keyframes by MuxH264ToPS.
	if !bytes.Contains(firstPayload, []byte{0x00, 0x00, 0x01, 0xBB}) {
		t.Fatal("first playback frame does not contain PSM (not a keyframe)")
	}

	// Pacing: inter-packet gaps approximate the 100ms frame interval.
	for i := 1; i < len(arrivals); i++ {
		gap := arrivals[i].Sub(arrivals[i-1])
		if gap < 40*time.Millisecond || gap > 250*time.Millisecond {
			t.Errorf("frame gap %d = %v, want ~100ms (paced)", i, gap)
		}
	}
}

// TestServer_DownloadStreamsWithoutPacing verifies a Download INVITE sends
// all frames as fast as possible (no pacing) and starts from a keyframe.
func TestServer_DownloadStreamsWithoutPacing(t *testing.T) {
	dir := t.TempDir()
	frames := []synthFrame{{0, true}}
	for i := 1; i < 10; i++ {
		frames = append(frames, synthFrame{time.Duration(i) * 100 * time.Millisecond, false})
	}
	segs := writeSyntheticRecording(t, dir, frames)
	idx := &testRecordingIndex{root: dir, segments: segs}

	_, sipPort, cancel := startPlaybackServer(t, idx)
	defer cancel()

	mediaSock, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatalf("bind media socket: %v", err)
	}
	defer mediaSock.Close()
	mediaPort := mediaSock.LocalAddr().(*net.UDPAddr).Port

	invite := buildPlaybackInvite("test-call-dl@example.com", "Download", segs[0].StartMS/1000, segs[0].EndMS/1000+1, mediaPort)
	clientConn, err := net.DialUDP("udp", nil, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: sipPort})
	if err != nil {
		t.Fatalf("dial server: %v", err)
	}
	defer clientConn.Close()

	if _, err := clientConn.Write(invite.Serialize()); err != nil {
		t.Fatalf("send INVITE: %v", err)
	}

	respBuf := make([]byte, 4096)
	clientConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, _, err := clientConn.ReadFromUDP(respBuf)
	if err != nil {
		t.Fatalf("read 200 OK: %v", err)
	}
	resp, err := Parse(respBuf[:n])
	if err != nil {
		t.Fatalf("parse 200 OK: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
	}
	if !containsSubstring(resp.Body, "s=Download") {
		t.Fatalf("SDP answer does not echo s=Download:\n%s", resp.Body)
	}

	// All frames must arrive well under the 1s recording duration.
	start := time.Now()
	var firstPayload []byte
	mediaSock.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, 2048)
	for i := 0; i < len(frames); i++ {
		n, _, err := mediaSock.ReadFromUDP(buf)
		if err != nil {
			t.Fatalf("read RTP frame %d: %v", i, err)
		}
		if i == 0 {
			firstPayload = append([]byte(nil), buf[:n]...)
		}
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("download took %v, want < 500ms (no pacing)", elapsed)
	}
	if !bytes.Contains(firstPayload, []byte{0x00, 0x00, 0x01, 0xBB}) {
		t.Fatal("first download frame does not contain PSM (not a keyframe)")
	}
}

// TestServer_ByeStopsPlaybackStreaming verifies BYE cancels the playback
// goroutine: no RTP arrives after the BYE is processed.
func TestServer_ByeStopsPlaybackStreaming(t *testing.T) {
	dir := t.TempDir()
	frames := []synthFrame{{0, true}}
	for i := 1; i < 20; i++ {
		frames = append(frames, synthFrame{time.Duration(i) * 100 * time.Millisecond, false})
	}
	segs := writeSyntheticRecording(t, dir, frames)
	idx := &testRecordingIndex{root: dir, segments: segs}

	_, sipPort, cancel := startPlaybackServer(t, idx)
	defer cancel()

	mediaSock, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatalf("bind media socket: %v", err)
	}
	defer mediaSock.Close()
	mediaPort := mediaSock.LocalAddr().(*net.UDPAddr).Port

	invite := buildPlaybackInvite("test-call-bye-pb@example.com", "Playback", segs[0].StartMS/1000, segs[0].EndMS/1000+1, mediaPort)
	clientConn, err := net.DialUDP("udp", nil, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: sipPort})
	if err != nil {
		t.Fatalf("dial server: %v", err)
	}
	defer clientConn.Close()

	if _, err := clientConn.Write(invite.Serialize()); err != nil {
		t.Fatalf("send INVITE: %v", err)
	}

	respBuf := make([]byte, 4096)
	clientConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, _, err := clientConn.ReadFromUDP(respBuf); err != nil {
		t.Fatalf("read 200 OK: %v", err)
	}

	// Receive 3 frames (stream is flowing), then BYE.
	mediaSock.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, 2048)
	for i := 0; i < 3; i++ {
		if _, _, err := mediaSock.ReadFromUDP(buf); err != nil {
			t.Fatalf("read frame %d: %v", i, err)
		}
	}
	bye := buildBye("test-call-bye-pb@example.com",
		"<sip:34020000012000000001@3402000000>;tag=pb001",
		"<sip:34020000001320000001@3402000000>",
		"<sip:34020000012000000001@127.0.0.1:5060>")
	if _, err := clientConn.Write(bye.Serialize()); err != nil {
		t.Fatalf("send BYE: %v", err)
	}

	// No more RTP within 400ms.
	mediaSock.SetReadDeadline(time.Now().Add(400 * time.Millisecond))
	for {
		if _, _, err := mediaSock.ReadFromUDP(buf); err != nil {
			break // timeout — streaming stopped, good
		}
		t.Fatal("RTP still arriving after BYE")
	}
}

// TestServer_PlaybackFastForwardsToKeyframe verifies that a mid-GOP start
// fast-forwards to the next keyframe: the first sent frame contains PSM.
func TestServer_PlaybackFastForwardsToKeyframe(t *testing.T) {
	dir := t.TempDir()
	frames := []synthFrame{{0, true}}
	for i := 1; i < 20; i++ {
		frames = append(frames, synthFrame{time.Duration(i) * 100 * time.Millisecond, false})
	}
	// Second keyframe at 2s.
	frames = append(frames, synthFrame{2000 * time.Millisecond, true})
	for i := 21; i < 30; i++ {
		frames = append(frames, synthFrame{time.Duration(i) * 100 * time.Millisecond, false})
	}
	segs := writeSyntheticRecording(t, dir, frames)
	idx := &testRecordingIndex{root: dir, segments: segs}

	_, sipPort, cancel := startPlaybackServer(t, idx)
	defer cancel()

	mediaSock, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatalf("bind media socket: %v", err)
	}
	defer mediaSock.Close()
	mediaPort := mediaSock.LocalAddr().(*net.UDPAddr).Port

	// Request playback starting 1s in (mid-GOP, non-keyframe).
	invite := buildPlaybackInvite("test-call-ff@example.com", "Playback", segs[0].StartMS/1000+1, segs[0].EndMS/1000+1, mediaPort)
	clientConn, err := net.DialUDP("udp", nil, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: sipPort})
	if err != nil {
		t.Fatalf("dial server: %v", err)
	}
	defer clientConn.Close()

	if _, err := clientConn.Write(invite.Serialize()); err != nil {
		t.Fatalf("send INVITE: %v", err)
	}

	respBuf := make([]byte, 4096)
	clientConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, _, err := clientConn.ReadFromUDP(respBuf); err != nil {
		t.Fatalf("read 200 OK: %v", err)
	}

	// First received frame must be the keyframe at 2s (contains PSM).
	mediaSock.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, 2048)
	n, _, err := mediaSock.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("read first frame: %v", err)
	}
	if !bytes.Contains(buf[:n], []byte{0x00, 0x00, 0x01, 0xBB}) {
		t.Fatal("first frame after mid-GOP start is not a keyframe (no PSM)")
	}
}

// TestServer_PlaybackInviteWithoutIndexGets488 verifies a Playback INVITE
// with no recording index is rejected with 488 Not Acceptable Here.
func TestServer_PlaybackInviteWithoutIndexGets488(t *testing.T) {
	_, sipPort, cancel := startPlaybackServer(t, nil)
	defer cancel()

	clientConn, err := net.DialUDP("udp", nil, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: sipPort})
	if err != nil {
		t.Fatalf("dial server: %v", err)
	}
	defer clientConn.Close()

	invite := buildPlaybackInvite("test-call-488a@example.com", "Playback", 1, 2, 60000)
	if _, err := clientConn.Write(invite.Serialize()); err != nil {
		t.Fatalf("send INVITE: %v", err)
	}

	respBuf := make([]byte, 4096)
	clientConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, _, err := clientConn.ReadFromUDP(respBuf)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	resp, err := Parse(respBuf[:n])
	if err != nil {
		t.Fatalf("parse response: %v", err)
	}
	if resp.StatusCode != 488 {
		t.Fatalf("expected 488 Not Acceptable Here, got %d", resp.StatusCode)
	}
}

// TestServer_PlaybackInviteWithoutCoveringSegmentsGets488 verifies a
// Playback INVITE whose t= range overlaps no recordings is rejected with 488.
func TestServer_PlaybackInviteWithoutCoveringSegmentsGets488(t *testing.T) {
	dir := t.TempDir()
	segs := writeSyntheticRecording(t, dir, []synthFrame{{0, true}})
	idx := &testRecordingIndex{root: dir, segments: segs}

	_, sipPort, cancel := startPlaybackServer(t, idx)
	defer cancel()

	clientConn, err := net.DialUDP("udp", nil, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: sipPort})
	if err != nil {
		t.Fatalf("dial server: %v", err)
	}
	defer clientConn.Close()

	// t= range far in the past (2001) — no overlap with the recording.
	invite := buildPlaybackInvite("test-call-488b@example.com", "Playback", 1000000000, 1000000100, 60000)
	if _, err := clientConn.Write(invite.Serialize()); err != nil {
		t.Fatalf("send INVITE: %v", err)
	}

	respBuf := make([]byte, 4096)
	clientConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, _, err := clientConn.ReadFromUDP(respBuf)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	resp, err := Parse(respBuf[:n])
	if err != nil {
		t.Fatalf("parse response: %v", err)
	}
	if resp.StatusCode != 488 {
		t.Fatalf("expected 488 Not Acceptable Here, got %d", resp.StatusCode)
	}
}

// buildInfo builds a synthetic SIP INFO request carrying a MANSCDP body.
func buildInfo(callID, from, to, contact, body string) SipMessage {
	return SipMessage{
		Method:      "INFO",
		RequestURI:  "sip:3402000000@3402000000",
		From:        from,
		To:          to,
		CallID:      callID,
		CSeq:        "3 INFO",
		Contact:     contact,
		Via:         "SIP/2.0/UDP 192.168.1.100:5060;rport;branch=z9hG4bK-24680",
		ContentType: "Application/MANSCDP+xml",
		Body:        body,
		Headers:     make(map[string]string),
	}
}

// sendInfo writes an INFO request on conn and reads the server's 200 OK.
func sendInfo(t *testing.T, conn *net.UDPConn, info SipMessage) {
	t.Helper()
	if _, err := conn.Write(info.Serialize()); err != nil {
		t.Fatalf("send INFO: %v", err)
	}
	buf := make([]byte, 4096)
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, _, err := conn.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("read INFO 200 OK: %v", err)
	}
	resp, err := Parse(buf[:n])
	if err != nil {
		t.Fatalf("parse INFO 200 OK: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200 OK for INFO, got %d", resp.StatusCode)
	}
}

// playbackControlBody builds a PlaybackControl INFO body.
func playbackControlBody(value, startTime, endTime, speed, scale string) string {
	info := ""
	if value != "" {
		info += "<ControlValue>" + value + "</ControlValue>"
	}
	if startTime != "" {
		info += "<StartTime>" + startTime + "</StartTime>"
	}
	if endTime != "" {
		info += "<EndTime>" + endTime + "</EndTime>"
	}
	if speed != "" {
		info += "<Speed>" + speed + "</Speed>"
	}
	if scale != "" {
		info += "<Scale>" + scale + "</Scale>"
	}
	return "<Control><CmdType>PlaybackControl</CmdType><SN>1</SN><DeviceID>d</DeviceID><Info>" + info + "</Info></Control>"
}

// TestServer_PlaybackPauseStopsRtpAndPlayResumes verifies a PAUSE INFO
// halts RTP delivery and a subsequent PLAY INFO resumes it.
func TestServer_PlaybackPauseStopsRtpAndPlayResumes(t *testing.T) {
	dir := t.TempDir()
	frames := []synthFrame{{0, true}}
	for i := 1; i < 20; i++ {
		frames = append(frames, synthFrame{time.Duration(i) * 100 * time.Millisecond, false})
	}
	segs := writeSyntheticRecording(t, dir, frames)
	idx := &testRecordingIndex{root: dir, segments: segs}

	_, sipPort, cancel := startPlaybackServer(t, idx)
	defer cancel()

	mediaSock, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatalf("bind media socket: %v", err)
	}
	defer mediaSock.Close()
	mediaPort := mediaSock.LocalAddr().(*net.UDPAddr).Port

	invite := buildPlaybackInvite("test-call-pause@example.com", "Playback", segs[0].StartMS/1000, segs[0].EndMS/1000+1, mediaPort)
	clientConn, err := net.DialUDP("udp", nil, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: sipPort})
	if err != nil {
		t.Fatalf("dial server: %v", err)
	}
	defer clientConn.Close()

	if _, err := clientConn.Write(invite.Serialize()); err != nil {
		t.Fatalf("send INVITE: %v", err)
	}
	respBuf := make([]byte, 4096)
	clientConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, _, err := clientConn.ReadFromUDP(respBuf); err != nil {
		t.Fatalf("read 200 OK: %v", err)
	}

	// Receive 3 frames to confirm streaming is flowing.
	mediaSock.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, 2048)
	for i := 0; i < 3; i++ {
		if _, _, err := mediaSock.ReadFromUDP(buf); err != nil {
			t.Fatalf("read frame %d: %v", i, err)
		}
	}

	// Send PAUSE.
	sendInfo(t, clientConn, buildInfo("test-call-pause@example.com",
		"<sip:34020000012000000001@3402000000>;tag=pb001",
		"<sip:34020000001320000001@3402000000>",
		"<sip:34020000012000000001@127.0.0.1:5060>",
		playbackControlBody("PAUSE", "", "", "", "")))

	// No RTP within 300ms after PAUSE.
	mediaSock.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	for {
		if _, _, err := mediaSock.ReadFromUDP(buf); err != nil {
			break // timeout — paused, good
		}
		t.Fatal("RTP still arriving after PAUSE")
	}

	// Send PLAY to resume.
	sendInfo(t, clientConn, buildInfo("test-call-pause@example.com",
		"<sip:34020000012000000001@3402000000>;tag=pb001",
		"<sip:34020000001320000001@3402000000>",
		"<sip:34020000012000000001@127.0.0.1:5060>",
		playbackControlBody("PLAY", "", "", "", "")))

	// RTP resumes: next packet arrives within 500ms.
	mediaSock.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	if _, _, err := mediaSock.ReadFromUDP(buf); err != nil {
		t.Fatalf("no RTP after PLAY resume: %v", err)
	}
}

// TestServer_PlaybackSeekToKeyframe verifies a PLAY INFO with StartTime
// seeks mid-recording: the first packet after the seek is a keyframe (PSM).
func TestServer_PlaybackSeekToKeyframe(t *testing.T) {
	dir := t.TempDir()
	frames := []synthFrame{{0, true}}
	for i := 1; i < 20; i++ {
		frames = append(frames, synthFrame{time.Duration(i) * 100 * time.Millisecond, false})
	}
	// Second keyframe at 2s.
	frames = append(frames, synthFrame{2000 * time.Millisecond, true})
	for i := 21; i < 30; i++ {
		frames = append(frames, synthFrame{time.Duration(i) * 100 * time.Millisecond, false})
	}
	segs := writeSyntheticRecording(t, dir, frames)
	idx := &testRecordingIndex{root: dir, segments: segs}

	_, sipPort, cancel := startPlaybackServer(t, idx)
	defer cancel()

	mediaSock, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatalf("bind media socket: %v", err)
	}
	defer mediaSock.Close()
	mediaPort := mediaSock.LocalAddr().(*net.UDPAddr).Port

	invite := buildPlaybackInvite("test-call-seek@example.com", "Playback", segs[0].StartMS/1000, segs[0].EndMS/1000+1, mediaPort)
	clientConn, err := net.DialUDP("udp", nil, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: sipPort})
	if err != nil {
		t.Fatalf("dial server: %v", err)
	}
	defer clientConn.Close()

	if _, err := clientConn.Write(invite.Serialize()); err != nil {
		t.Fatalf("send INVITE: %v", err)
	}
	respBuf := make([]byte, 4096)
	clientConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, _, err := clientConn.ReadFromUDP(respBuf); err != nil {
		t.Fatalf("read 200 OK: %v", err)
	}

	// Receive 3 frames (stream flowing from the start).
	mediaSock.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, 2048)
	for i := 0; i < 3; i++ {
		if _, _, err := mediaSock.ReadFromUDP(buf); err != nil {
			t.Fatalf("read frame %d: %v", i, err)
		}
	}

	// Seek to the keyframe at 2s (base + 2000ms).
	seekMs := segs[0].StartMS + 2000
	sendInfo(t, clientConn, buildInfo("test-call-seek@example.com",
		"<sip:34020000012000000001@3402000000>;tag=pb001",
		"<sip:34020000001320000001@3402000000>",
		"<sip:34020000012000000001@127.0.0.1:5060>",
		playbackControlBody("PLAY", formatGBTime(seekMs), "", "", "")))

	// First packet after seek must be a keyframe (PSM present).
	mediaSock.SetReadDeadline(time.Now().Add(5 * time.Second))
	n, _, err := mediaSock.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("read first frame after seek: %v", err)
	}
	if !bytes.Contains(buf[:n], []byte{0x00, 0x00, 0x01, 0xBB}) {
		t.Fatal("first frame after seek is not a keyframe (no PSM)")
	}
}

// TestServer_PlaybackSpeed4x verifies a Speed=4 control paces frames at
// ~25ms (100ms nominal / 4) instead of the recorded 100ms interval.
func TestServer_PlaybackSpeed4x(t *testing.T) {
	dir := t.TempDir()
	frames := []synthFrame{{0, true}}
	for i := 1; i < 40; i++ {
		frames = append(frames, synthFrame{time.Duration(i) * 100 * time.Millisecond, false})
	}
	segs := writeSyntheticRecording(t, dir, frames)
	idx := &testRecordingIndex{root: dir, segments: segs}

	_, sipPort, cancel := startPlaybackServer(t, idx)
	defer cancel()

	mediaSock, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatalf("bind media socket: %v", err)
	}
	defer mediaSock.Close()
	mediaPort := mediaSock.LocalAddr().(*net.UDPAddr).Port

	invite := buildPlaybackInvite("test-call-speed@example.com", "Playback", segs[0].StartMS/1000, segs[0].EndMS/1000+1, mediaPort)
	clientConn, err := net.DialUDP("udp", nil, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: sipPort})
	if err != nil {
		t.Fatalf("dial server: %v", err)
	}
	defer clientConn.Close()

	if _, err := clientConn.Write(invite.Serialize()); err != nil {
		t.Fatalf("send INVITE: %v", err)
	}
	respBuf := make([]byte, 4096)
	clientConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, _, err := clientConn.ReadFromUDP(respBuf); err != nil {
		t.Fatalf("read 200 OK: %v", err)
	}

	// Receive 3 frames at nominal 100ms pacing.
	mediaSock.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, 2048)
	for i := 0; i < 3; i++ {
		if _, _, err := mediaSock.ReadFromUDP(buf); err != nil {
			t.Fatalf("read frame %d: %v", i, err)
		}
	}

	// Send Speed=4 control.
	sendInfo(t, clientConn, buildInfo("test-call-speed@example.com",
		"<sip:34020000012000000001@3402000000>;tag=pb001",
		"<sip:34020000001320000001@3402000000>",
		"<sip:34020000012000000001@127.0.0.1:5060>",
		playbackControlBody("PLAY", "", "", "4", "")))

	// Measure inter-packet gaps after the speed change. The first frame
	// after the control re-anchors pacing; measure subsequent gaps.
	var arrivals []time.Time
	mediaSock.SetReadDeadline(time.Now().Add(5 * time.Second))
	for len(arrivals) < 6 {
		if _, _, err := mediaSock.ReadFromUDP(buf); err != nil {
			t.Fatalf("read frame after speed change: %v", err)
		}
		arrivals = append(arrivals, time.Now())
	}
	var total time.Duration
	for i := 2; i < len(arrivals); i++ {
		gap := arrivals[i].Sub(arrivals[i-1])
		total += gap
		if gap > 60*time.Millisecond {
			t.Errorf("gap %d = %v, want ~25ms at 4x speed", i, gap)
		}
	}
	if avg := total / time.Duration(len(arrivals)-2); avg > 45*time.Millisecond {
		t.Errorf("avg gap = %v, want ~25ms at 4x speed", avg)
	}
}

// TestServer_InfoOnLiveSessionAcknowledged verifies a PAUSE INFO on a live
// (s=Play) session is acknowledged with 200 OK and live RTP keeps flowing.
func TestServer_InfoOnLiveSessionAcknowledged(t *testing.T) {
	hub := NewFrameHub()
	cfg := Config{
		DeviceID:              "34020000001320000001",
		ChannelID:             "34020000001320000001",
		SIPDomain:             "3402000000",
		Password:              "12345678",
		LocalSIPPort:          0,
		PlatformSIPAddress:    "127.0.0.1",
		PlatformSIPPort:       15070,
		RegisterIntervalSecs:  60,
		HeartbeatIntervalSecs: 60,
		HeartbeatTimeoutCount: 3,
	}
	server := New(cfg, DeviceInfo{}, hub)
	server.SetTestMode()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	go func() { _ = server.Start(ctx) }()
	time.Sleep(100 * time.Millisecond)
	sipPort := waitSIPPort(t, server)

	mediaSock, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatalf("bind media socket: %v", err)
	}
	defer mediaSock.Close()
	mediaPort := mediaSock.LocalAddr().(*net.UDPAddr).Port

	sdp := fmt.Sprintf("v=0\no=- 0 0 IN IP4 127.0.0.1\ns=Play\nc=IN IP4 127.0.0.1\nt=0 0\nm=video %d RTP/AVP 96\na=rtpmap:96 PS/90000\na=recvonly\ny=0100000001", mediaPort)
	invite := buildInvite("test-call-live@example.com",
		"<sip:34020000012000000001@3402000000>;tag=live001",
		"<sip:34020000001320000001@3402000000>",
		"<sip:34020000012000000001@127.0.0.1:5060>")
	invite.Body = sdp
	clientConn, err := net.DialUDP("udp", nil, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: sipPort})
	if err != nil {
		t.Fatalf("dial server: %v", err)
	}
	defer clientConn.Close()

	if _, err := clientConn.Write(invite.Serialize()); err != nil {
		t.Fatalf("send INVITE: %v", err)
	}
	respBuf := make([]byte, 4096)
	clientConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, _, err := clientConn.ReadFromUDP(respBuf); err != nil {
		t.Fatalf("read 200 OK: %v", err)
	}

	// Wait for subscribe, then push AUs so live RTP flows.
	time.Sleep(200 * time.Millisecond)
	if count := hub.SubscriberCount(); count != 1 {
		t.Fatalf("expected 1 subscriber after INVITE, got %d", count)
	}
	for i := 0; i < 3; i++ {
		hub.Write(AccessUnit{
			NALUs:     []NALU{{Type: 5, Data: []byte{0x65, 0x11, 0x22, 0x33}, IsIDR: true}},
			Timestamp: time.Now(),
			KeyFrame:  true,
		})
		time.Sleep(50 * time.Millisecond)
	}

	// Receive a couple RTP packets to confirm live flow.
	mediaSock.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 2048)
	for i := 0; i < 2; i++ {
		if _, _, err := mediaSock.ReadFromUDP(buf); err != nil {
			t.Fatalf("read live RTP frame %d: %v", i, err)
		}
	}

	// Send PAUSE INFO on the live session — server must 200 OK and keep streaming.
	sendInfo(t, clientConn, buildInfo("test-call-live@example.com",
		"<sip:34020000012000000001@3402000000>;tag=live001",
		"<sip:34020000001320000001@3402000000>",
		"<sip:34020000012000000001@127.0.0.1:5060>",
		playbackControlBody("PAUSE", "", "", "", "")))

	// Live RTP continues: push more AUs and assert they arrive.
	for i := 0; i < 3; i++ {
		hub.Write(AccessUnit{
			NALUs:     []NALU{{Type: 5, Data: []byte{0x65, 0x11, 0x22, 0x33}, IsIDR: true}},
			Timestamp: time.Now(),
			KeyFrame:  true,
		})
		time.Sleep(50 * time.Millisecond)
	}
	mediaSock.SetReadDeadline(time.Now().Add(2 * time.Second))
	for i := 0; i < 2; i++ {
		if _, _, err := mediaSock.ReadFromUDP(buf); err != nil {
			t.Fatalf("live RTP stopped after PAUSE INFO (frame %d): %v", i, err)
		}
	}
}

// TestServer_InfoNonPlaybackControlGets200 verifies an INFO with a
// non-PlaybackControl body is acknowledged with 200 OK.
func TestServer_InfoNonPlaybackControlGets200(t *testing.T) {
	_, sipPort, cancel := startPlaybackServer(t, nil)
	defer cancel()

	clientConn, err := net.DialUDP("udp", nil, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: sipPort})
	if err != nil {
		t.Fatalf("dial server: %v", err)
	}
	defer clientConn.Close()

	sendInfo(t, clientConn, buildInfo("test-call-info@example.com",
		"<sip:34020000012000000001@3402000000>;tag=info001",
		"<sip:34020000001320000001@3402000000>",
		"<sip:34020000012000000001@127.0.0.1:5060>",
		`<Control><CmdType>DeviceControl</CmdType><SN>1</SN><DeviceID>d</DeviceID><Info><ControlValue>PAUSE</ControlValue></Info></Control>`))
}
