package device

import (
	"bufio"
	"context"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"
)

// ─── parseSDP media transport (issue #14) ──────────────────────────────────

func TestParseSDP_MediaTransport(t *testing.T) {
	cases := []struct {
		name    string
		sdp     string
		want    sdpMediaTransport
		wantErr bool
	}{
		{
			name: "udp offer",
			sdp: "v=0\r\no=- 0 0 IN IP4 192.168.1.10\r\ns=Play\r\nc=IN IP4 192.168.1.10\r\nt=0 0\r\n" +
				"m=video 30000 RTP/AVP 96\r\ny=2000000001\r\n",
			want: mediaUDP,
		},
		{
			name: "tcp passive offer — device connects",
			sdp: "v=0\r\no=- 0 0 IN IP4 192.168.1.10\r\ns=Play\r\nc=IN IP4 192.168.1.10\r\nt=0 0\r\n" +
				"m=video 30000 TCP/RTP/AVP 96\r\na=recvonly\r\na=setup:passive\r\na=connection:new\r\ny=2000000001\r\n",
			want: mediaTCPConnect,
		},
		{
			name: "tcp actpass offer — device chooses to connect",
			sdp: "v=0\r\no=- 0 0 IN IP4 192.168.1.10\r\ns=Play\r\nc=IN IP4 192.168.1.10\r\nt=0 0\r\n" +
				"m=video 30000 TCP/RTP/AVP 96\r\na=setup:actpass\r\ny=2000000001\r\n",
			want: mediaTCPConnect,
		},
		{
			name: "tcp setup:active offer — platform dials device",
			sdp: "v=0\r\no=- 0 0 IN IP4 192.168.1.10\r\ns=Play\r\nc=IN IP4 192.168.1.10\r\nt=0 0\r\n" +
				"m=video 9 TCP/RTP/AVP 96\r\na=setup:active\r\ny=2000000001\r\n",
			want: mediaTCPListen,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, _, mt, err := parseSDP(tc.sdp)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got transport %v", mt)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseSDP failed: %v", err)
			}
			if mt != tc.want {
				t.Fatalf("expected transport %v, got %v", tc.want, mt)
			}
		})
	}
}

// ─── TCP $-framing wire format (issue #14 follow-up) ──────────────────────

// GB28181 platforms demux RTSP-interleaved style framing: '$' + channel +
// 2-byte big-endian length — a 4-byte header.
func TestWriteRTPOverTCP_FourByteHeader(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	rtp := []byte{0x80, 0x60, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01}

	go func() {
		_ = writeRTPOverTCP(server, rtp)
	}()

	client.SetReadDeadline(time.Now().Add(2 * time.Second))
	head := make([]byte, 5)
	if _, err := ioReadFull(client, head); err != nil {
		t.Fatalf("read framing header: %v", err)
	}
	if head[0] != 0x24 {
		t.Fatalf("framing byte: want 0x24 '$', got %#x", head[0])
	}
	if head[1] != 0x00 {
		t.Fatalf("channel byte: want 0x00, got %#x", head[1])
	}
	length := int(head[2])<<8 | int(head[3])
	if length != len(rtp) {
		t.Fatalf("length prefix: want %d, got %d", len(rtp), length)
	}
	if head[4]&0xc0 != 0x80 {
		t.Fatalf("RTP version bits: want 10xxxxxx, got %#08b", head[4])
	}
}

func ioReadFull(c net.Conn, buf []byte) (int, error) {
	total := 0
	for total < len(buf) {
		n, err := c.Read(buf[total:])
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

// ─── INVITE behavior (issue #14) ───────────────────────────────────────────

// startTestServer boots a test-mode server on an ephemeral SIP port and
// returns it with its SIP port and a FrameHub the test can feed.
func startTestServer(t *testing.T) (*Server, int, *FrameHub) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.Cleanup(cancel)

	hub := NewFrameHub()
	cfg := Config{
		DeviceID:              "34020000001320000001",
		ChannelID:             "34020000001320000001",
		SIPDomain:             "3402000000",
		Password:              "12345678",
		LocalSIPPort:          0,
		PlatformSIPAddress:    "127.0.0.1",
		PlatformSIPPort:       15060,
		RegisterIntervalSecs:  60,
		HeartbeatIntervalSecs: 60,
		HeartbeatTimeoutCount: 3,
		Transport:             "udp",
	}
	server := New(cfg, DeviceInfo{}, hub)
	server.SetTestMode()
	go func() { _ = server.Start(ctx) }()
	sipPort := waitSIPPort(t, server)
	return server, sipPort, hub
}

func sendInviteForSDP(t *testing.T, sipPort int, sdpBody string) string {
	t.Helper()
	invite := buildInvite(
		"tcp-media-"+fmtShort(),
		"<sip:34020000002000000001@3402000000>;tag=plat",
		"<sip:34020000001320000001@3402000000>",
		"<sip:34020000002000000001@127.0.0.1:5060>",
	)
	invite.Body = sdpBody

	conn, err := net.DialUDP("udp", nil, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: sipPort})
	if err != nil {
		t.Fatalf("dial SIP: %v", err)
	}
	defer conn.Close()
	if _, err := conn.Write(invite.Serialize()); err != nil {
		t.Fatalf("send INVITE: %v", err)
	}

	buf := make([]byte, 65535)
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	return string(buf[:n])
}

func fmtShort() string { return time.Now().Format("150405.000000000") }

// A tcp-passive offer must be answered with setup:active and the device
// must dial the offered media port, sending 4-byte-framed RTP.
func TestInviteTCPPassiveConnectsAndFrames(t *testing.T) {
	_, sipPort, hub := startTestServer(t)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()
	mediaPort := listener.Addr().(*net.TCPAddr).Port

	sdpBody := "v=0\r\no=- 0 0 IN IP4 127.0.0.1\r\ns=Play\r\nc=IN IP4 127.0.0.1\r\nt=0 0\r\n" +
		"m=video " + strconv.Itoa(mediaPort) + " TCP/RTP/AVP 96\r\na=setup:passive\r\na=connection:new\r\ny=2000000001\r\n"

	resp := sendInviteForSDP(t, sipPort, sdpBody)
	if !strings.Contains(resp, "200 OK") {
		t.Fatalf("want 200 OK, got: %.120s", resp)
	}
	if !strings.Contains(resp, "TCP/RTP/AVP 96") {
		t.Fatalf("answer SDP must echo TCP transport: %.200s", resp)
	}
	if !strings.Contains(resp, "a=setup:active") {
		t.Fatalf("answer SDP must declare setup:active: %.200s", resp)
	}

	// Device must dial the offered port.
	type accepted struct {
		conn net.Conn
	}
	ch := make(chan accepted, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			close(ch)
			return
		}
		ch <- accepted{conn}
	}()
	select {
	case a, ok := <-ch:
		if !ok {
			t.Fatal("accept failed")
		}
		defer a.conn.Close()

		// Wait for the device's hub subscription, then feed keyframes until
		// a framed packet arrives (a Write before Subscribe registers would
		// be dropped — the hub only reaches current subscribers).
		subDeadline := time.Now().Add(3 * time.Second)
		for hub.SubscriberCount() == 0 && time.Now().Before(subDeadline) {
			time.Sleep(2 * time.Millisecond)
		}
		if hub.SubscriberCount() == 0 {
			t.Fatal("device never subscribed to the hub")
		}
		go func() {
			for range time.Tick(20 * time.Millisecond) {
				hub.Write(AccessUnit{
					NALUs:    []NALU{{Type: 5, Data: []byte{0x65, 0x88, 0x84, 0x21, 0xa0}, IsIDR: true}},
					KeyFrame: true,
				})
			}
		}()
		// Feed one keyframe; expect 4-byte $-framing + RTP.
		hub.Write(AccessUnit{
			NALUs:    []NALU{{Type: 5, Data: []byte{0x65, 0x88, 0x84, 0x21, 0xa0}, IsIDR: true}},
			KeyFrame: true,
		})
		rd := bufio.NewReader(a.conn)
		a.conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		head := make([]byte, 5)
		if _, err := ioReadFullBuf(rd, head); err != nil {
			t.Fatalf("read framed RTP: %v", err)
		}
		if head[0] != 0x24 {
			t.Fatalf("framing byte: want 0x24, got %#x", head[0])
		}
		if head[1] != 0x00 {
			t.Fatalf("channel byte: want 0x00, got %#x", head[1])
		}
		if head[4]&0xc0 != 0x80 {
			t.Fatalf("RTP version bits: got %#08b", head[4])
		}
	case <-time.After(3 * time.Second):
		t.Fatal("device never connected TCP media")
	}
}

func ioReadFullBuf(rd *bufio.Reader, buf []byte) (int, error) {
	total := 0
	for total < len(buf) {
		n, err := rd.Read(buf[total:])
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

// setup:active offers (platform dials the device) are refused with 488.
func TestInviteTCPSetupActiveReturns488(t *testing.T) {
	_, sipPort, _ := startTestServer(t)

	sdpBody := "v=0\r\no=- 0 0 IN IP4 127.0.0.1\r\ns=Play\r\nc=IN IP4 127.0.0.1\r\nt=0 0\r\n" +
		"m=video 9 TCP/RTP/AVP 96\r\na=setup:active\r\ny=2000000001\r\n"

	resp := sendInviteForSDP(t, sipPort, sdpBody)
	if !strings.Contains(resp, "488") {
		t.Fatalf("want 488, got: %.120s", resp)
	}
}

func itoa(n int) string {
	return strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(string(rune(0)), "", ""), "", "")) // placeholder
}
