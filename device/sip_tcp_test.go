package device

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

// TestServer_TCPTransport_HandlesFramedSIP verifies the TCP transport path:
// a Content-Length framed SIP request over TCP is parsed and answered with a
// Content-Length framed 200 OK (GB/T 28181 Annex C.1 framing).
func TestServer_TCPTransport_HandlesFramedSIP(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	hub := NewFrameHub()
	cfg := Config{
		DeviceID:              "34020000001320000001",
		ChannelID:             "34020000001320000001",
		SIPDomain:             "3402000000",
		Password:              "12345678",
		LocalSIPPort:          0, // ephemeral
		PlatformSIPAddress:    "127.0.0.1",
		PlatformSIPPort:       15060,
		RegisterIntervalSecs:  60,
		HeartbeatIntervalSecs: 60,
		HeartbeatTimeoutCount: 3,
		Transport:             "tcp",
	}
	server := New(cfg, DeviceInfo{}, hub)
	server.SetTestMode()

	serverErr := make(chan error, 1)
	go func() {
		if err := server.Start(ctx); err != nil && !errors.Is(err, context.Canceled) {
			serverErr <- err
		}
	}()

	// Wait for the TCP listener to be up and grab its actual port.
	var tcpPort int
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if p, err := server.SIPTCPPort(); err == nil {
			tcpPort = p
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if tcpPort == 0 {
		t.Fatal("TCP listener did not start")
	}
	t.Logf("TCP listener on port %d", tcpPort)

	// Connect and send a Content-Length framed MESSAGE (Keepalive query).
	conn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", tcpPort))
	if err != nil {
		t.Fatalf("failed to dial TCP: %v", err)
	}
	defer conn.Close()

	body := `<Notify CmdType="Keepalive" SN="1"><DeviceID>34020000001320000001</DeviceID><Status>OK</Status></Notify>`
	msg := SipMessage{
		Method:      "MESSAGE",
		RequestURI:  "sip:3402000000@3402000000",
		From:        "<sip:34020000002000000001@3402000000>;tag=platty",
		To:          "<sip:34020000001320000001@3402000000>",
		CallID:      "tcp-harness-1@example.com",
		CSeq:        "1 MESSAGE",
		Via:         "SIP/2.0/TCP 127.0.0.1:15060;branch=z9hG4bK-tcp-harness",
		MaxForwards: "70",
		ContentType: "Application/MANSCDP+xml",
		Body:        body,
		Headers:     make(map[string]string),
	}
	wire := msg.Serialize()
	// Serialize already emits Content-Length; assert framing is present.
	if !strings.Contains(string(wire), "Content-Length:") {
		t.Fatal("serialized request missing Content-Length header")
	}
	if _, err := conn.Write(wire); err != nil {
		t.Fatalf("failed to write request: %v", err)
	}

	// Read the response with Content-Length framing.
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	reader := bufio.NewReader(conn)
	statusLine, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("failed to read status line: %v", err)
	}
	if !strings.Contains(statusLine, "SIP/2.0 200") {
		t.Fatalf("expected 200 OK, got %q", strings.TrimSpace(statusLine))
	}

	// Read headers until blank line, capture Content-Length.
	var contentLength int
	foundContentLength := false
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("failed to read header: %v", err)
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		if strings.HasPrefix(strings.ToLower(line), "content-length:") {
			parts := strings.SplitN(line, ":", 2)
			foundContentLength = true
			if _, err := fmt.Sscanf(strings.TrimSpace(parts[1]), "%d", &contentLength); err != nil {
				t.Fatalf("bad Content-Length %q: %v", parts[1], err)
			}
		}
	}
	if !foundContentLength {
		t.Fatal("response missing Content-Length header")
	}
	bodyBuf := make([]byte, contentLength)
	if _, err := reader.Read(bodyBuf); err != nil {
		t.Fatalf("failed to read response body: %v", err)
	}
	t.Logf("TCP response: %s (body %d bytes)", strings.TrimSpace(statusLine), contentLength)

	// Stop server.
	cancel()
	select {
	case err := <-serverErr:
		if err != nil {
			t.Fatalf("server error: %v", err)
		}
	case <-time.After(1 * time.Second):
		// Expected shutdown.
	}
}

// TestWriteRTPOverTCP_Framing verifies $-framing (Annex C.2, RTSP-interleaved
// style as demuxed by GB28181 platforms): each RTP packet is prefixed with
// 0x24 '$' + channel byte 0x00 + 2-byte big-endian length — a 4-byte header
// (issue #14).
func TestWriteRTPOverTCP_Framing(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	rtpPkt := []byte{0x80, 0x60, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01}
	done := make(chan error, 1)
	go func() {
		done <- writeRTPOverTCP(server, rtpPkt)
	}()

	buf := make([]byte, 4+len(rtpPkt))
	if _, err := io.ReadFull(client, buf); err != nil {
		t.Fatalf("read failed: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("write failed: %v", err)
	}

	if buf[0] != 0x24 {
		t.Fatalf("framing byte = 0x%02x, want 0x24 ('$')", buf[0])
	}
	if buf[1] != 0x00 {
		t.Fatalf("channel byte = 0x%02x, want 0x00", buf[1])
	}
	wantLen := len(rtpPkt)
	gotLen := int(buf[2])<<8 | int(buf[3])
	if gotLen != wantLen {
		t.Fatalf("length = %d, want %d", gotLen, wantLen)
	}
	for i, b := range rtpPkt {
		if buf[4+i] != b {
			t.Fatalf("payload byte %d = 0x%02x, want 0x%02x", i, buf[4+i], b)
		}
	}
}

// newFramingTestServer builds the minimal Server readSIPStream needs.
func newFramingTestServer(t *testing.T) *Server {
	t.Helper()
	return New(Config{DeviceID: "34020000001320000001", SIPDomain: "3402000000"}, DeviceInfo{}, NewFrameHub())
}

// startFramingReader runs readSIPStream and closes the server side when it
// returns, so the framing layer's drop decision surfaces as a clean EOF on
// the client end.
func startFramingReader(ctx context.Context, s *Server, server, client net.Conn) {
	go func() {
		readSIPStream(ctx, bufio.NewReader(server), server, s)
		server.Close()
	}()
}

// expectStreamClosed fails unless the server side of the pipe closes (the
// read loop returned) within the deadline — the framing layer's response to
// protocol abuse.
func expectStreamClosed(t *testing.T, client net.Conn) {
	t.Helper()
	client.SetReadDeadline(time.Now().Add(3 * time.Second))
	one := make([]byte, 1)
	for {
		if _, err := client.Read(one); err != nil {
			var netErr net.Error
			if errors.As(err, &netErr) && netErr.Timeout() {
				t.Fatal("server side did not close the connection within the deadline")
			}
			return // EOF or reset — server side closed
		}
	}
}

// TestReadSIPStreamOversizedContentLength pins issue #37: a forged
// Content-Length must not trigger an unbounded allocation — the connection
// is dropped instead.
func TestReadSIPStreamOversizedContentLength(t *testing.T) {
	server, client := net.Pipe()
	defer client.Close()
	s := newFramingTestServer(t)
	startFramingReader(context.Background(), s, server, client)

	msg := "MESSAGE sip:3402000000@3402000000 SIP/2.0\r\n" +
		"Content-Length: 1073741824\r\n\r\n"
	if _, err := client.Write([]byte(msg)); err != nil {
		t.Fatalf("write: %v", err)
	}
	expectStreamClosed(t, client)
}

// TestReadSIPStreamCustomLimit verifies MaxSIPMessageSize is honored:
// a body within a small configured limit passes framing, an oversized one
// drops the connection.
func TestReadSIPStreamCustomLimit(t *testing.T) {
	s := newFramingTestServer(t)
	s.cfg.MaxSIPMessageSize = 128 // the good message fits (75 B), bad does not

	server, client := net.Pipe()
	defer client.Close()
	startFramingReader(context.Background(), s, server, client)

	// A total message (headers + body) under the limit survives framing.
	// A response is used so dispatch (handleResponse) is pipe-safe.
	ok := "SIP/2.0 200 OK\r\nCSeq: 1 REGISTER\r\nCall-ID: limit@t\r\nContent-Length: 4\r\n\r\nbody"
	if _, err := client.Write([]byte(ok)); err != nil {
		t.Fatalf("write ok: %v", err)
	}
	// Give the reader a moment to consume the good message before the bad
	// one, so the drop below is attributable to the limit, not framing
	// desync from the previous test step.
	time.Sleep(100 * time.Millisecond)

	bad := "MESSAGE sip:x SIP/2.0\r\nContent-Length: 5000\r\n\r\nbody"
	if _, err := client.Write([]byte(bad)); err != nil {
		t.Fatalf("write bad: %v", err)
	}
	expectStreamClosed(t, client)
}

// TestReadSIPStreamHeaderFlood pins the header-side of issue #37: endless
// header lines without the terminating empty line must not accumulate
// unboundedly.
func TestReadSIPStreamHeaderFlood(t *testing.T) {
	server, client := net.Pipe()
	defer client.Close()
	s := newFramingTestServer(t)
	startFramingReader(context.Background(), s, server, client)

	for range 20000 {
		if _, err := client.Write([]byte("X-Flood: aaaaaaaaaaaaaaaaaaaaaaaaaaaa\r\n")); err != nil {
			break // server dropped us — expected
		}
	}
	expectStreamClosed(t, client)
}

// TestReadSIPStreamShortBody verifies short bodies are not silently
// truncated into corrupted messages (io.ReadFull semantics): the stream
// sync is lost, so the connection is dropped.
func TestReadSIPStreamShortBody(t *testing.T) {
	server, client := net.Pipe()
	defer client.Close()
	s := newFramingTestServer(t)
	startFramingReader(context.Background(), s, server, client)

	msg := "MESSAGE sip:x SIP/2.0\r\nContent-Length: 100\r\n\r\nshort"
	if _, err := client.Write([]byte(msg)); err != nil {
		t.Fatalf("write: %v", err)
	}
	// Keep the pipe open; the reader must hit its deadline waiting for the
	// missing body bytes and drop the connection rather than dispatch a
	// truncated message.
	expectStreamClosed(t, client)
}

// TestGetLocalIPHonorsCancelledContext pins issue #58: the route probe
// dials through the lifecycle context, so a cancelled context aborts the
// probe immediately instead of completing the dial.
func TestGetLocalIPHonorsCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := getLocalIP(ctx, "127.0.0.1:5060"); !errors.Is(err, context.Canceled) {
		t.Fatalf("getLocalIP with a cancelled context = %v, want context.Canceled", err)
	}
}
