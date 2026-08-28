package device

import (
	"bufio"
	"context"
	"fmt"
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
		if err := server.Start(ctx); err != nil && err != context.Canceled {
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

// TestWriteRTPOverTCP_Framing verifies $-framing (Annex C.2): each RTP packet
// is prefixed with 0x24 '$' + 2-byte big-endian length.
func TestWriteRTPOverTCP_Framing(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	rtpPkt := []byte{0x80, 0x60, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01}
	done := make(chan error, 1)
	go func() {
		done <- writeRTPOverTCP(server, rtpPkt)
	}()

	buf := make([]byte, 3+len(rtpPkt))
	if _, err := client.Read(buf); err != nil {
		t.Fatalf("read failed: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("write failed: %v", err)
	}

	if buf[0] != 0x24 {
		t.Fatalf("framing byte = 0x%02x, want 0x24 ('$')", buf[0])
	}
	wantLen := len(rtpPkt)
	gotLen := int(buf[1])<<8 | int(buf[2])
	if gotLen != wantLen {
		t.Fatalf("length = %d, want %d", gotLen, wantLen)
	}
	for i, b := range rtpPkt {
		if buf[3+i] != b {
			t.Fatalf("payload byte %d = 0x%02x, want 0x%02x", i, buf[3+i], b)
		}
	}
}
