package device_test

// Wire-level tests for GB/T 28181-2022 §9.2 voice talkback receive
// (issue #80): a real device Server over real UDP sockets — platform
// INVITEs in, 200/488 answers out, G.711 RTP audio delivered to the host
// sink. The Go twin of gb28181-rs's talkback suite; goldens verified
// against the same reference, including the byte-exact production NVR
// offer form (GoSIP / MiBeeNvr M5: payload-type-only PCMA without
// a=rtpmap, leading-zero decimal y= SSRC, no trailing CRLF).

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mickeyzzc/gb28181-go/device"
)

// talkPacket is one delivery captured by talkRecorder.
type talkPacket struct {
	payload []byte
	ssrc    uint32
	codec   device.AudioCodec
}

// talkRecorder captures talkback deliveries fired on the receive goroutine.
type talkRecorder struct {
	mu  sync.Mutex
	got []talkPacket
}

func (r *talkRecorder) OnAudio(payload []byte, ssrc uint32, codec device.AudioCodec) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.got = append(r.got, talkPacket{payload: append([]byte(nil), payload...), ssrc: ssrc, codec: codec})
}

func (r *talkRecorder) packets() []talkPacket {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]talkPacket(nil), r.got...)
}

// audioInvite builds a plain audio-only INVITE (m=audio, no m=video) —
// the §9.2 talkback offer form.
func audioInvite(callID string, mLine string, y string) device.SipMessage {
	body := "v=0\r\n" +
		"o=- 0 0 IN IP4 127.0.0.1\r\n" +
		"s=Play\r\n" +
		"c=IN IP4 127.0.0.1\r\n" +
		"t=0 0\r\n" +
		mLine + "\r\n" +
		"a=sendonly\r\n" +
		"y=" + y + "\r\n"
	return device.SipMessage{
		Method:      "INVITE",
		RequestURI:  "sip:34020000001320000001@3402000000",
		From:        "<sip:34020000002000000001@3402000000>;tag=plat" + callID,
		To:          "<sip:34020000001320000001@3402000000>",
		CallID:      callID,
		CSeq:        "1 INVITE",
		Via:         "SIP/2.0/UDP 127.0.0.1:5060;branch=z9hG4bKtalk" + callID,
		ContentType: "application/sdp",
		Body:        body,
		UserAgent:   "fakeplatform",
		Headers:     map[string]string{},
	}
}

// realNVRTalkbackInvite is the byte-exact production NVR talkback INVITE
// (GoSIP / MiBeeNvr M5, captured 2026-09-11 — the same offer the Rust
// twin pins): quoted display names, no From tag, Via host 0.0.0.0,
// o= session-id 0 0, payload-type-only PCMA (no a=rtpmap), leading-zero
// decimal y= SSRC, and no trailing CRLF after y=. Adapted to the harness
// device/loopback addresses with a recomputed Content-Length.
func realNVRTalkbackInvite(devPort int) []byte {
	body := "v=0\r\n" +
		"o=34020000002000000001 0 0 IN IP4 127.0.0.1\r\n" +
		"s=Play\r\n" +
		"c=IN IP4 127.0.0.1\r\n" +
		"t=0 0\r\n" +
		"m=audio 57411 RTP/AVP 8\r\n" +
		"a=sendrecv\r\n" +
		"y=0200006001"
	return []byte(fmt.Sprintf("INVITE sip:34020000001320000001@127.0.0.1:%d SIP/2.0\r\n"+
		"Via: SIP/2.0/UDP 0.0.0.0:5060;branch=z9hG4bK.Ot9zCQf07bLFwIQhoFUtpQpa1Sia4bx5\r\n"+
		"CSeq: 1 INVITE\r\n"+
		"From: \"34020000002000000001\" <sip:34020000002000000001@127.0.0.1>\r\n"+
		"To: \"34020000001320000001\" <sip:34020000001320000001@127.0.0.1:%d>\r\n"+
		"Call-ID: 4BS50Dp5zTahRVgCDQsCoDMkZzHy4hWB\r\n"+
		"Contact: <sip:34020000002000000001@127.0.0.1:5060>\r\n"+
		"Max-Forwards: 70\r\n"+
		"Content-Type: application/sdp\r\n"+
		"User-Agent: GoSIP\r\n"+
		"Subject: 34020000001320000001:0200006001,34020000002000000001:0\r\n"+
		"Content-Length: %d\r\n"+
		"Allow: INVITE, ACK, CANCEL, REGISTER, MESSAGE, BYE, INFO, NOTIFY, OPTIONS\r\n"+
		"\r\n"+
		"%s", devPort, devPort, len(body), body))
}

// audioPortFromAnswer extracts the receive port from the m=audio line of
// a 200 OK SDP answer.
func audioPortFromAnswer(t *testing.T, body string) int {
	t.Helper()
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "m=audio ") {
			port, err := strconv.Atoi(strings.Fields(line)[1])
			if err != nil {
				t.Fatalf("answer m=audio port: %v", err)
			}
			return port
		}
	}
	t.Fatalf("answer has no m=audio line: %q", body)
	return 0
}

// rtpPCMA builds one RTP packet: version 2, PT 8, the given SSRC and
// payload. csrc adds a 1-entry CSRC list (CC=1) to prove header stripping.
func rtpPCMA(ssrc uint32, payload []byte, csrc bool) []byte {
	b0 := byte(0x80)
	if csrc {
		b0 = 0x81
	}
	pkt := []byte{
		b0, 0x08, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00,
		byte(ssrc >> 24), byte(ssrc >> 16), byte(ssrc >> 8), byte(ssrc),
	}
	if csrc {
		pkt = append(pkt, 0xDE, 0xAD, 0xBE, 0xEF)
	}
	return append(pkt, payload...)
}

// A talkback INVITE with no sink registered is refused with 488 —
// receiving audio nobody consumes would be a silent black hole.
func TestTalkbackInviteWithoutSinkReturns488(t *testing.T) {
	platConn, devAddr := startWireTestServer(t, func(*device.Server) {})

	writeSnapMsg(t, platConn, audioInvite("talk-nosink-1", "m=audio 30000 RTP/AVP 8", "777"), devAddr)
	resp, _ := readSnapMsg(t, platConn)
	if resp.StatusCode != 488 {
		t.Fatalf("status = %d, want 488", resp.StatusCode)
	}
}

// A talkback INVITE whose codec is not G.711 A/μ-law is refused with 488.
func TestTalkbackInviteNonG711Returns488(t *testing.T) {
	platConn, devAddr := startWireTestServer(t, func(srv *device.Server) {
		srv.SetTalkbackSink(&talkRecorder{})
	})

	writeSnapMsg(t, platConn, audioInvite("talk-l16-1", "m=audio 30000 RTP/AVP 96", "777"), devAddr)
	resp, _ := readSnapMsg(t, platConn)
	if resp.StatusCode != 488 {
		t.Fatalf("status = %d, want 488", resp.StatusCode)
	}
}

// A talkback INVITE asking for TCP media is refused with 488 — this
// revision receives talkback over UDP only.
func TestTalkbackInviteTCPMediaReturns488(t *testing.T) {
	platConn, devAddr := startWireTestServer(t, func(srv *device.Server) {
		srv.SetTalkbackSink(&talkRecorder{})
	})

	writeSnapMsg(t, platConn, audioInvite("talk-tcp-1", "m=audio 30000 TCP/RTP/AVP 8", "777"), devAddr)
	resp, _ := readSnapMsg(t, platConn)
	if resp.StatusCode != 488 {
		t.Fatalf("status = %d, want 488", resp.StatusCode)
	}
}

// Full talkback loop: audio INVITE → 200 OK with the m=audio answer →
// platform streams RTP at the answered port → the sink receives the
// G.711 payload with the session SSRC and codec. The answer body is
// byte-exact against the Rust twin's golden (build_audio_sdp_answer).
func TestTalkbackInviteAnswersAndDeliversRTP(t *testing.T) {
	sink := &talkRecorder{}
	platConn, devAddr := startWireTestServer(t, func(srv *device.Server) {
		srv.SetTalkbackSink(sink)
	})

	writeSnapMsg(t, platConn, audioInvite("talk-e2e-1", "m=audio 30000 RTP/AVP 8", "777"), devAddr)
	resp, _ := readSnapMsg(t, platConn)
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.ContentType; ct != "application/sdp" {
		t.Fatalf("Content-Type = %q, want application/sdp", ct)
	}
	port := audioPortFromAnswer(t, resp.Body)
	wantBody := fmt.Sprintf("v=0\r\no=- 0 0 IN IP4 127.0.0.1\r\ns=Play\r\nc=IN IP4 127.0.0.1\r\nt=0 0\r\n"+
		"m=audio %d RTP/AVP 8\r\na=rtpmap:8 PCMA/8000\r\ny=777\r\n", port)
	if resp.Body != wantBody {
		t.Fatalf("answer body mismatch:\n got %q\nwant %q", resp.Body, wantBody)
	}

	// Plain packet: PT 8, SSRC 777 in the header.
	if _, err := platConn.WriteToUDP(rtpPCMA(777, []byte{0xD5, 0x5A, 0xA5}, false),
		&net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: port}); err != nil {
		t.Fatalf("send rtp: %v", err)
	}
	// CSRC packet (CC=1): the 4 CSRC bytes must not leak into the payload.
	if _, err := platConn.WriteToUDP(rtpPCMA(777, []byte{0x37}, true),
		&net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: port}); err != nil {
		t.Fatalf("send rtp csrc: %v", err)
	}

	waitFor(t, 5*time.Second, func() bool { return len(sink.packets()) == 2 })
	got := sink.packets()
	if string(got[0].payload) != "\xD5\x5A\xA5" || got[0].ssrc != 777 || got[0].codec != device.AudioPCMA {
		t.Fatalf("delivery 0 = %+v, want payload D5 5A A5 / ssrc 777 / PCMA", got[0])
	}
	if string(got[1].payload) != "7" || got[1].ssrc != 777 || got[1].codec != device.AudioPCMA {
		t.Fatalf("delivery 1 = %+v, want CSRC-stripped payload / ssrc 777 / PCMA", got[1])
	}
}

// The PCMU twin: payload type 0 answers a=rtpmap:0 PCMU/8000 and the
// negotiated codec travels with every delivery.
func TestTalkbackPCMUAnswersAndDelivers(t *testing.T) {
	sink := &talkRecorder{}
	platConn, devAddr := startWireTestServer(t, func(srv *device.Server) {
		srv.SetTalkbackSink(sink)
	})

	writeSnapMsg(t, platConn, audioInvite("talk-pcmu-1", "m=audio 30000 RTP/AVP 0", "888"), devAddr)
	resp, _ := readSnapMsg(t, platConn)
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	port := audioPortFromAnswer(t, resp.Body)
	if !strings.Contains(resp.Body, "m=audio "+strconv.Itoa(port)+" RTP/AVP 0\r\n") ||
		!strings.Contains(resp.Body, "a=rtpmap:0 PCMU/8000\r\n") ||
		!strings.Contains(resp.Body, "y=888\r\n") {
		t.Fatalf("PCMU answer body: %q", resp.Body)
	}

	if _, err := platConn.WriteToUDP(rtpPCMA(888, []byte{0xFF}, false),
		&net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: port}); err != nil {
		t.Fatalf("send rtp: %v", err)
	}
	waitFor(t, 5*time.Second, func() bool { return len(sink.packets()) == 1 })
	got := sink.packets()[0]
	if got.codec != device.AudioPCMU || got.ssrc != 888 || string(got.payload) != "\xFF" {
		t.Fatalf("delivery = %+v, want payload FF / ssrc 888 / PCMU", got)
	}
}

// Golden full loop with the byte-exact production NVR offer: the real
// message must answer 200 OK (not 488) and deliver RTP with the
// leading-zero SSRC parsed as decimal 200006001. The answer echoes the
// session SSRC numerically (leading zero dropped) and the To tag is
// appended to the quoted display-name form.
func TestRealNVRTalkbackInviteFullLoop(t *testing.T) {
	sink := &talkRecorder{}
	platConn, devAddr := startWireTestServer(t, func(srv *device.Server) {
		srv.SetTalkbackSink(sink)
	})

	if _, err := platConn.WriteToUDP(realNVRTalkbackInvite(devAddr.Port), devAddr); err != nil {
		t.Fatalf("send real NVR INVITE: %v", err)
	}
	resp, _ := readSnapMsg(t, platConn)
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if !strings.HasPrefix(resp.To, "\"34020000001320000001\" <sip:") || !strings.Contains(resp.To, ";tag=") {
		t.Fatalf("To header = %q, want quoted display name with appended tag", resp.To)
	}
	port := audioPortFromAnswer(t, resp.Body)
	wantBody := fmt.Sprintf("v=0\r\no=- 0 0 IN IP4 127.0.0.1\r\ns=Play\r\nc=IN IP4 127.0.0.1\r\nt=0 0\r\n"+
		"m=audio %d RTP/AVP 8\r\na=rtpmap:8 PCMA/8000\r\ny=200006001\r\n", port)
	if resp.Body != wantBody {
		t.Fatalf("answer body mismatch:\n got %q\nwant %q", resp.Body, wantBody)
	}

	// RTP with SSRC 200006001 = 0x0BEBD971 — the sink must see the packet
	// SSRC, not the leading-zero string form.
	if _, err := platConn.WriteToUDP(rtpPCMA(200006001, []byte{0xD5, 0x5A, 0xA5}, false),
		&net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: port}); err != nil {
		t.Fatalf("send rtp: %v", err)
	}
	waitFor(t, 5*time.Second, func() bool { return len(sink.packets()) == 1 })
	got := sink.packets()[0]
	if string(got.payload) != "\xD5\x5A\xA5" || got.ssrc != 200006001 || got.codec != device.AudioPCMA {
		t.Fatalf("delivery = %+v, want payload D5 5A A5 / ssrc 200006001 / PCMA", got)
	}
}

// BYE tears the talkback receiver down through the shared media cleanup:
// after BYE the answered port goes quiet (the receive goroutine exits
// with its socket).
func TestTalkbackBYETearsDownReceiver(t *testing.T) {
	sink := &talkRecorder{}
	platConn, devAddr := startWireTestServer(t, func(srv *device.Server) {
		srv.SetTalkbackSink(sink)
	})

	writeSnapMsg(t, platConn, audioInvite("talk-bye-1", "m=audio 30000 RTP/AVP 8", "777"), devAddr)
	resp, _ := readSnapMsg(t, platConn)
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	port := audioPortFromAnswer(t, resp.Body)

	writeSnapMsg(t, platConn, device.SipMessage{
		Method:     "BYE",
		RequestURI: "sip:34020000001320000001@3402000000",
		From:       "<sip:34020000002000000001@3402000000>;tag=plattalk-bye-1",
		To:         resp.To,
		CallID:     "talk-bye-1",
		CSeq:       "2 BYE",
		Via:        "SIP/2.0/UDP 127.0.0.1:5060;branch=z9hG4bKtalkbye",
		Headers:    map[string]string{},
	}, devAddr)
	byeOK, _ := readSnapMsg(t, platConn)
	if byeOK.StatusCode != 200 {
		t.Fatalf("BYE status = %d, want 200", byeOK.StatusCode)
	}

	// Drain a moment, then fire a late RTP packet at the dead session and
	// confirm nothing is delivered afterwards.
	time.Sleep(200 * time.Millisecond)
	n := len(sink.packets())
	if _, err := platConn.WriteToUDP(rtpPCMA(777, []byte{0x00}, false),
		&net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: port}); err != nil {
		t.Fatalf("send late rtp: %v", err)
	}
	time.Sleep(500 * time.Millisecond)
	if got := len(sink.packets()); got != n {
		t.Fatalf("deliveries after BYE = %d, want %d", got, n)
	}
}
