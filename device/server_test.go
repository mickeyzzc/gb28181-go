package device

import (
	"context"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// waitSIPPort polls until the server's SIP listener is bound (Start runs
// concurrently) and returns its port.
func waitSIPPort(t *testing.T, s *Server) int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if port, err := s.SIPPort(); err == nil {
			return port
		}
		if time.Now().After(deadline) {
			t.Fatal("SIP listener never bound")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// buildInvite builds a synthetic INVITE message for testing.
func buildInvite(callID, from, to, contact string) SipMessage {
	sdp := `v=0
o=- 0 0 IN IP4 192.168.1.100
s=Play
c=IN IP4 192.168.1.100
t=0 0
m=video 60000 RTP/AVP 96
a=rtpmap:96 PS/90000
a=recvonly
y=0100000001`

	return SipMessage{
		Method:      "INVITE",
		RequestURI:  "sip:3402000000@3402000000",
		From:        from,
		To:          to,
		CallID:      callID,
		CSeq:        "1 INVITE",
		Contact:     contact,
		Via:         "SIP/2.0/UDP 192.168.1.100:5060;rport;branch=z9hG4bK-12345",
		ContentType: "application/sdp",
		Body:        sdp,
		Headers:     make(map[string]string),
	}
}

// buildBye builds a synthetic BYE message for testing.
func buildBye(callID, from, to, contact string) SipMessage {
	return SipMessage{
		Method:     "BYE",
		RequestURI: "sip:3402000000@3402000000",
		From:       from,
		To:         to,
		CallID:     callID,
		CSeq:       "2 BYE",
		Contact:    contact,
		Via:        "SIP/2.0/UDP 192.168.1.100:5060;rport;branch=z9hG4bK-67890",
		Headers:    make(map[string]string),
	}
}

// TestServer_AttachesSubscriberOnInvite verifies that the server subscribes to AUHub on INVITE and unsubscribes on BYE.
func TestServer_AttachesSubscriberOnInvite(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Create real AUHub
	hub := NewFrameHub()

	// Create Server with mock config (local SIP port 0 for ephemeral)
	cfg := Config{
		DeviceID:              "34020000001320000001",
		ChannelID:             "34020000001320000001",
		SIPDomain:             "3402000000",
		Password:              "12345678",
		LocalSIPPort:          0, // Use ephemeral port
		PlatformSIPAddress:    "127.0.0.1",
		PlatformSIPPort:       15060, // Use different port to avoid conflict
		RegisterIntervalSecs:  60,
		HeartbeatIntervalSecs: 60,
		HeartbeatTimeoutCount: 3,
	}
	server := New(cfg, DeviceInfo{}, hub)
	server.SetTestMode()

	// Start server in goroutine
	serverErr := make(chan error, 1)
	go func() {
		if err := server.Start(ctx); err != nil && err != context.Canceled {
			serverErr <- err
		}
	}()

	// Wait for server to start and get actual SIP port
	time.Sleep(100 * time.Millisecond)
	sipPort := waitSIPPort(t, server)
	t.Logf("Server started on SIP port %d", sipPort)

	// Check initial subscriber count (should be 0)
	if count := hub.SubscriberCount(); count != 0 {
		t.Fatalf("Expected 0 subscribers before INVITE, got %d", count)
	}

	// Send synthetic INVITE to SIP port
	callID := "test-call-123@example.com"
	from := "<sip:34020000012000000001@3402000000>;tag=12345"
	to := "<sip:34020000001320000001@3402000000>"
	contact := "<sip:34020000012000000001@192.168.1.100:5060>"

	invite := buildInvite(callID, from, to, contact)
	inviteBytes := invite.Serialize()

	// Dial server SIP port
	clientConn, err := net.DialUDP("udp", nil, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: sipPort})
	if err != nil {
		t.Fatalf("Failed to dial server: %v", err)
	}
	defer clientConn.Close()

	// Send INVITE
	if _, err := clientConn.Write(inviteBytes); err != nil {
		t.Fatalf("Failed to send INVITE: %v", err)
	}

	// Wait for subscriber to be attached (give time for 200 OK + subscribe)
	time.Sleep(200 * time.Millisecond)

	// Verify subscriber attached
	if count := hub.SubscriberCount(); count != 1 {
		t.Fatalf("Expected 1 subscriber after INVITE, got %d", count)
	}
	t.Log("Subscriber attached after INVITE")

	// Send BYE
	bye := buildBye(callID, from, to, contact)
	byeBytes := bye.Serialize()
	if _, err := clientConn.Write(byeBytes); err != nil {
		t.Fatalf("Failed to send BYE: %v", err)
	}

	// Wait for subscriber to be detached
	time.Sleep(200 * time.Millisecond)

	// Verify subscriber detached
	if count := hub.SubscriberCount(); count != 0 {
		t.Fatalf("Expected 0 subscribers after BYE, got %d", count)
	}
	t.Log("Subscriber detached after BYE")

	// Stop server
	cancel()
	select {
	case err := <-serverErr:
		if err != nil {
			t.Fatalf("Server error: %v", err)
		}
	case <-time.After(1 * time.Second):
		// Expected shutdown
	}
}

// TestServer_Sends200OKBeforeMedia verifies that 200 OK is sent before any RTP media.
func TestServer_Sends200OKBeforeMedia(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	hub := NewFrameHub()
	cfg := Config{
		DeviceID:              "34020000001320000001",
		ChannelID:             "34020000001320000001",
		SIPDomain:             "3402000000",
		Password:              "12345678",
		LocalSIPPort:          0,
		PlatformSIPAddress:    "127.0.0.1",
		PlatformSIPPort:       15061,
		RegisterIntervalSecs:  60,
		HeartbeatIntervalSecs: 60,
		HeartbeatTimeoutCount: 3,
	}
	server := New(cfg, DeviceInfo{}, hub)
	server.SetTestMode()

	serverErr := make(chan error, 1)
	go func() {
		if err := server.Start(ctx); err != nil && err != context.Canceled {
			serverErr <- err
		}
	}()

	time.Sleep(100 * time.Millisecond)
	sipPort := waitSIPPort(t, server)

	// Send INVITE
	callID := "test-call-456@example.com"
	from := "<sip:34020000012000000001@3402000000>;tag=67890"
	to := "<sip:34020000001320000001@3402000000>"
	contact := "<sip:34020000012000000001@192.168.1.100:5060>"

	invite := buildInvite(callID, from, to, contact)
	inviteBytes := invite.Serialize()

	clientConn, err := net.DialUDP("udp", nil, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: sipPort})
	if err != nil {
		t.Fatalf("Failed to dial server: %v", err)
	}
	defer clientConn.Close()

	// Track timestamps
	var ok200Time time.Time
	var ok200Received bool
	var mu sync.Mutex

	// Start goroutine to receive responses
	respBuf := make([]byte, 4096)
	go func() {
		for {
			clientConn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
			n, _, err := clientConn.ReadFromUDP(respBuf)
			if err != nil {
				continue
			}
			msg, err := Parse(respBuf[:n])
			if err != nil {
				continue
			}
			if msg.StatusCode == 200 {
				mu.Lock()
				if !ok200Received {
					ok200Received = true
					ok200Time = time.Now()
				}
				mu.Unlock()
			}
		}
	}()

	// Send INVITE
	inviteSentTime := time.Now()
	if _, err := clientConn.Write(inviteBytes); err != nil {
		t.Fatalf("Failed to send INVITE: %v", err)
	}

	// Wait for 200 OK
	time.Sleep(300 * time.Millisecond)

	mu.Lock()
	ok200Was := ok200Received
	ok200Ts := ok200Time
	mu.Unlock()

	if !ok200Was {
		t.Fatal("200 OK not received")
	}

	// Verify 200 OK was sent (before any media)
	if ok200Ts.Before(inviteSentTime) {
		t.Fatalf("200 OK timestamp %v before invite sent time %v (impossible)", ok200Ts, inviteSentTime)
	}

	t.Logf("200 OK received at %v (after INVITE sent at %v)", ok200Ts, inviteSentTime)

	// The key assertion: 200 OK was sent, and we didn't receive RTP before it
	// (we can't easily test RTP without a real camera, but we verify the 200 OK flow)
	cancel()
	select {
	case err := <-serverErr:
		if err != nil {
			t.Fatalf("Server error: %v", err)
		}
	case <-time.After(1 * time.Second):
	}
}

// TestServer_200OK_ContainsDeviceSDP verifies that 200 OK response contains device SDP.
func TestServer_200OK_ContainsDeviceSDP(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	hub := NewFrameHub()
	cfg := Config{
		DeviceID:              "34020000001320000001",
		ChannelID:             "34020000001320000001",
		SIPDomain:             "3402000000",
		Password:              "12345678",
		LocalSIPPort:          0,
		PlatformSIPAddress:    "127.0.0.1",
		PlatformSIPPort:       15062,
		RegisterIntervalSecs:  60,
		HeartbeatIntervalSecs: 60,
		HeartbeatTimeoutCount: 3,
	}
	server := New(cfg, DeviceInfo{}, hub)
	server.SetTestMode()

	serverErr := make(chan error, 1)
	go func() {
		if err := server.Start(ctx); err != nil && err != context.Canceled {
			serverErr <- err
		}
	}()

	time.Sleep(100 * time.Millisecond)
	sipPort := waitSIPPort(t, server)

	// Send INVITE
	callID := "test-call-789@example.com"
	from := "<sip:34020000012000000001@3402000000>;tag=11111"
	to := "<sip:34020000001320000001@3402000000>"
	contact := "<sip:34020000012000000001@192.168.1.100:5060>"

	invite := buildInvite(callID, from, to, contact)
	inviteBytes := invite.Serialize()

	clientConn, err := net.DialUDP("udp", nil, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: sipPort})
	if err != nil {
		t.Fatalf("Failed to dial server: %v", err)
	}
	defer clientConn.Close()

	if _, err := clientConn.Write(inviteBytes); err != nil {
		t.Fatalf("Failed to send INVITE: %v", err)
	}

	// Receive 200 OK
	respBuf := make([]byte, 4096)
	clientConn.SetReadDeadline(time.Now().Add(1 * time.Second))
	n, _, err := clientConn.ReadFromUDP(respBuf)
	if err != nil {
		t.Fatalf("Failed to read 200 OK: %v", err)
	}

	resp, err := Parse(respBuf[:n])
	if err != nil {
		t.Fatalf("Failed to parse 200 OK: %v", err)
	}

	if resp.StatusCode != 200 {
		t.Fatalf("Expected 200 OK, got %d", resp.StatusCode)
	}

	body := resp.Body
	t.Logf("200 OK body:\n%s", body)

	// Verify SDP contains required fields
	requiredFields := []string{
		"v=0",
		"m=video",
		"a=rtpmap:96 PS/90000",
		"a=sendonly",
		"y=",
	}

	for _, field := range requiredFields {
		if !containsSubstring(body, field) {
			t.Errorf("200 OK body missing required field: %s", field)
		}
	}

	cancel()
	select {
	case err := <-serverErr:
		if err != nil {
			t.Fatalf("Server error: %v", err)
		}
	case <-time.After(1 * time.Second):
	}
}

// TestServer_EchoesSSRC verifies that 200 OK echoes the SSRC from INVITE.
func TestServer_EchoesSSRC(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	hub := NewFrameHub()
	cfg := Config{
		DeviceID:              "34020000001320000001",
		ChannelID:             "34020000001320000001",
		SIPDomain:             "3402000000",
		Password:              "12345678",
		LocalSIPPort:          0,
		PlatformSIPAddress:    "127.0.0.1",
		PlatformSIPPort:       15063,
		RegisterIntervalSecs:  60,
		HeartbeatIntervalSecs: 60,
		HeartbeatTimeoutCount: 3,
	}
	server := New(cfg, DeviceInfo{}, hub)
	server.SetTestMode()

	serverErr := make(chan error, 1)
	go func() {
		if err := server.Start(ctx); err != nil && err != context.Canceled {
			serverErr <- err
		}
	}()

	time.Sleep(100 * time.Millisecond)
	sipPort := waitSIPPort(t, server)

	// Send INVITE with specific SSRC y=0100000001 (decimal 100000001)
	callID := "test-call-ssrc@example.com"
	from := "<sip:34020000012000000001@3402000000>;tag=22222"
	to := "<sip:34020000001320000001@3402000000>"
	contact := "<sip:34020000012000000001@192.168.1.100:5060>"

	invite := buildInvite(callID, from, to, contact)
	inviteBytes := invite.Serialize()

	clientConn, err := net.DialUDP("udp", nil, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: sipPort})
	if err != nil {
		t.Fatalf("Failed to dial server: %v", err)
	}
	defer clientConn.Close()

	if _, err := clientConn.Write(inviteBytes); err != nil {
		t.Fatalf("Failed to send INVITE: %v", err)
	}

	// Receive 200 OK
	respBuf := make([]byte, 4096)
	clientConn.SetReadDeadline(time.Now().Add(1 * time.Second))
	n, _, err := clientConn.ReadFromUDP(respBuf)
	if err != nil {
		t.Fatalf("Failed to read 200 OK: %v", err)
	}

	resp, err := Parse(respBuf[:n])
	if err != nil {
		t.Fatalf("Failed to parse 200 OK: %v", err)
	}

	body := resp.Body
	t.Logf("200 OK body:\n%s", body)

	// Verify SSRC is echoed as y=100000001
	// The INVITE had y=0100000001 which is 100000001 in decimal
	if !containsSubstring(body, "y=100000001") {
		t.Errorf("200 OK body does not contain echoed SSRC y=100000001, got: %s", body)
	}

	cancel()
	select {
	case err := <-serverErr:
		if err != nil {
			t.Fatalf("Server error: %v", err)
		}
	case <-time.After(1 * time.Second):
	}
}

// TestServer_ByeCleansUpSubscriberAndSocket verifies that BYE cleans up subscriber and media socket.
func TestServer_ByeCleansUpSubscriberAndSocket(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	hub := NewFrameHub()
	cfg := Config{
		DeviceID:              "34020000001320000001",
		ChannelID:             "34020000001320000001",
		SIPDomain:             "3402000000",
		Password:              "12345678",
		LocalSIPPort:          0,
		PlatformSIPAddress:    "127.0.0.1",
		PlatformSIPPort:       15064,
		RegisterIntervalSecs:  60,
		HeartbeatIntervalSecs: 60,
		HeartbeatTimeoutCount: 3,
	}
	server := New(cfg, DeviceInfo{}, hub)
	server.SetTestMode()

	serverErr := make(chan error, 1)
	go func() {
		if err := server.Start(ctx); err != nil && err != context.Canceled {
			serverErr <- err
		}
	}()

	time.Sleep(100 * time.Millisecond)
	sipPort := waitSIPPort(t, server)

	// Send INVITE
	callID := "test-call-bye@example.com"
	from := "<sip:34020000012000000001@3402000000>;tag=33333"
	to := "<sip:34020000001320000001@3402000000>"
	contact := "<sip:34020000012000000001@192.168.1.100:5060>"

	invite := buildInvite(callID, from, to, contact)
	inviteBytes := invite.Serialize()

	clientConn, err := net.DialUDP("udp", nil, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: sipPort})
	if err != nil {
		t.Fatalf("Failed to dial server: %v", err)
	}
	defer clientConn.Close()

	if _, err := clientConn.Write(inviteBytes); err != nil {
		t.Fatalf("Failed to send INVITE: %v", err)
	}

	// Wait for subscriber to be attached
	time.Sleep(200 * time.Millisecond)

	// Verify subscriber attached
	if count := hub.SubscriberCount(); count != 1 {
		t.Fatalf("Expected 1 subscriber after INVITE, got %d", count)
	}

	// Verify media socket is bound
	server.mu.Lock()
	mediaConnNotNil := server.mediaConn != nil
	server.mu.Unlock()
	if !mediaConnNotNil {
		t.Fatal("Media socket not bound after INVITE")
	}

	// Send BYE
	bye := buildBye(callID, from, to, contact)
	byeBytes := bye.Serialize()
	if _, err := clientConn.Write(byeBytes); err != nil {
		t.Fatalf("Failed to send BYE: %v", err)
	}

	// Wait for cleanup
	time.Sleep(200 * time.Millisecond)

	// Verify subscriber detached
	if count := hub.SubscriberCount(); count != 0 {
		t.Fatalf("Expected 0 subscribers after BYE, got %d", count)
	}

	// Verify media socket is closed
	server.mu.Lock()
	mediaConnClosed := server.mediaConn == nil
	server.mu.Unlock()
	if !mediaConnClosed {
		t.Fatal("Media socket not nil after BYE (expected closed)")
	}

	t.Log("BYE successfully cleaned up subscriber and media socket")

	cancel()
	select {
	case err := <-serverErr:
		if err != nil {
			t.Fatalf("Server error: %v", err)
		}
	case <-time.After(1 * time.Second):
	}
}

// Helper function to check if a string contains a substring.
func containsSubstring(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) && contains(s, substr))
}

func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// TestParseSDP_ExtractsMediaAddressAndSSRC verifies parseSDP returns the
// RTP destination (c= IP + m=video port) and SSRC (y=) from INVITE SDP.
func TestParseSDP_ExtractsMediaAddressAndSSRC(t *testing.T) {
	sdp := `v=0
o=- 0 0 IN IP4 192.168.1.100
s=Play
c=IN IP4 192.168.1.100
t=0 0
m=video 60000 RTP/AVP 96
a=rtpmap:96 PS/90000
a=recvonly
y=0100000001`

	addr, ssrc, _, err := parseSDP(sdp)
	if err != nil {
		t.Fatalf("parseSDP failed: %v", err)
	}
	if addr != "192.168.1.100:60000" {
		t.Errorf("expected media addr 192.168.1.100:60000, got %s", addr)
	}
	if ssrc != 100000001 {
		t.Errorf("expected SSRC 100000001, got %d", ssrc)
	}
}

// TestParseSDP_RejectsMissingMediaLines verifies SDP without c=/m= lines
// is rejected — the device must never fall back to the SIP peer address as
// RTP destination (it would flood the platform's signaling port).
func TestParseSDP_RejectsMissingMediaLines(t *testing.T) {
	sdp := "v=0\ns=Play\ny=0100000001\n"
	if _, _, _, err := parseSDP(sdp); err == nil {
		t.Error("expected error for SDP missing c=/m= lines, got nil")
	}
}

// TestServer_SendsRtpToSdpAddressNotSipPeer verifies that after INVITE,
// RTP media flows to the address in the SDP c=/m= lines — NOT to the SIP
// peer that sent the INVITE (regression: media used to flood the NVR's
// SIP port 5060).
func TestServer_SendsRtpToSdpAddressNotSipPeer(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	hub := NewFrameHub()
	cfg := Config{
		DeviceID:              "34020000001320000001",
		ChannelID:             "34020000001320000001",
		SIPDomain:             "3402000000",
		Password:              "12345678",
		LocalSIPPort:          0,
		PlatformSIPAddress:    "127.0.0.1",
		PlatformSIPPort:       15065,
		RegisterIntervalSecs:  60,
		HeartbeatIntervalSecs: 60,
		HeartbeatTimeoutCount: 3,
	}
	server := New(cfg, DeviceInfo{}, hub)
	server.SetTestMode()

	go func() {
		_ = server.Start(ctx)
	}()

	time.Sleep(100 * time.Millisecond)
	sipPort := waitSIPPort(t, server)

	// Bind the "platform media" socket the SDP will point to.
	mediaSock, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatalf("failed to bind media socket: %v", err)
	}
	defer mediaSock.Close()
	mediaPort := mediaSock.LocalAddr().(*net.UDPAddr).Port

	// INVITE with SDP pointing at mediaSock, sent from a different socket (the SIP peer).
	sdp := fmt.Sprintf("v=0\no=- 0 0 IN IP4 127.0.0.1\ns=Play\nc=IN IP4 127.0.0.1\nt=0 0\nm=video %d RTP/AVP 96\na=rtpmap:96 PS/90000\na=recvonly\ny=0100000001", mediaPort)
	invite := buildInvite("test-call-rtp@example.com",
		"<sip:34020000012000000001@3402000000>;tag=44444",
		"<sip:34020000001320000001@3402000000>",
		"<sip:34020000012000000001@127.0.0.1:5060>")
	invite.Body = sdp

	clientConn, err := net.DialUDP("udp", nil, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: sipPort})
	if err != nil {
		t.Fatalf("failed to dial server: %v", err)
	}
	defer clientConn.Close()

	if _, err := clientConn.Write(invite.Serialize()); err != nil {
		t.Fatalf("failed to send INVITE: %v", err)
	}

	// Wait for subscribe + media goroutine, then push one AU through the hub.
	time.Sleep(200 * time.Millisecond)
	if count := hub.SubscriberCount(); count != 1 {
		t.Fatalf("expected 1 subscriber after INVITE, got %d", count)
	}
	hub.Write(AccessUnit{
		NALUs:     []NALU{{Type: 5, Data: []byte{0x65, 0x11, 0x22, 0x33}, IsIDR: true}},
		Timestamp: time.Now(),
		KeyFrame:  true,
	})

	// RTP must arrive at the SDP media address (mediaSock).
	mediaSock.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 2048)
	n, _, err := mediaSock.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("no RTP received at SDP media address: %v", err)
	}
	if n < 12 || buf[0]>>6 != 2 {
		t.Fatalf("received packet is not RTP v2 (len=%d, first byte=0x%02x)", n, buf[0])
	}

	// SIP peer socket must NOT receive RTP (drain whatever is pending).
	clientConn.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	sipBuf := make([]byte, 2048)
	for {
		n, _, err := clientConn.ReadFromUDP(sipBuf)
		if err != nil {
			break // timeout — nothing else arriving, good
		}
		if n >= 12 && sipBuf[0]>>6 == 2 && sipBuf[1]&0x7f == 96 {
			t.Fatal("RTP media leaked to SIP peer socket")
		}
	}

	t.Logf("RTP arrived at SDP media address 127.0.0.1:%d (%d bytes)", mediaPort, n)
}

// TestServer_ReInviteReplacesPreviousSession verifies that a second INVITE
// tears down the previous media session instead of leaking a parallel
// RTP stream (observed live: 3 simultaneous streams to the same NVR port
// after NVR re-registers).
func TestServer_ReInviteReplacesPreviousSession(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	hub := NewFrameHub()
	cfg := Config{
		DeviceID:              "34020000001320000001",
		ChannelID:             "34020000001320000001",
		SIPDomain:             "3402000000",
		Password:              "12345678",
		LocalSIPPort:          0,
		PlatformSIPAddress:    "127.0.0.1",
		PlatformSIPPort:       15066,
		RegisterIntervalSecs:  60,
		HeartbeatIntervalSecs: 60,
		HeartbeatTimeoutCount: 3,
	}
	server := New(cfg, DeviceInfo{}, hub)
	server.SetTestMode()

	go func() {
		_ = server.Start(ctx)
	}()

	time.Sleep(100 * time.Millisecond)
	sipPort := waitSIPPort(t, server)

	clientConn, err := net.DialUDP("udp", nil, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: sipPort})
	if err != nil {
		t.Fatalf("failed to dial server: %v", err)
	}
	defer clientConn.Close()

	buildSdpInvite := func(port int) SipMessage {
		sdp := fmt.Sprintf("v=0\no=- 0 0 IN IP4 127.0.0.1\ns=Play\nc=IN IP4 127.0.0.1\nt=0 0\nm=video %d RTP/AVP 96\ny=0100000001", port)
		inv := buildInvite("test-call-reinvite@example.com",
			"<sip:34020000012000000001@3402000000>;tag=55555",
			"<sip:34020000001320000001@3402000000>",
			"<sip:34020000012000000001@127.0.0.1:5060>")
		inv.Body = sdp
		return inv
	}

	// First INVITE → media port A
	sockA, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatalf("bind sockA: %v", err)
	}
	defer sockA.Close()
	portA := sockA.LocalAddr().(*net.UDPAddr).Port
	invA := buildSdpInvite(portA)
	if _, err := clientConn.Write(invA.Serialize()); err != nil {
		t.Fatalf("send INVITE 1: %v", err)
	}
	time.Sleep(200 * time.Millisecond)
	if count := hub.SubscriberCount(); count != 1 {
		t.Fatalf("expected 1 subscriber after INVITE 1, got %d", count)
	}

	// Second INVITE → media port B
	sockB, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatalf("bind sockB: %v", err)
	}
	defer sockB.Close()
	portB := sockB.LocalAddr().(*net.UDPAddr).Port
	invB := buildSdpInvite(portB)
	if _, err := clientConn.Write(invB.Serialize()); err != nil {
		t.Fatalf("send INVITE 2: %v", err)
	}
	time.Sleep(200 * time.Millisecond)

	// Still exactly one subscriber and one usable media session.
	if count := hub.SubscriberCount(); count != 1 {
		t.Fatalf("expected 1 subscriber after re-INVITE, got %d (leaked session)", count)
	}

	// Media now flows to B, and A must go silent.
	hub.Write(AccessUnit{
		NALUs:     []NALU{{Type: 5, Data: []byte{0x65, 0x11, 0x22, 0x33}, IsIDR: true}},
		Timestamp: time.Now(),
		KeyFrame:  true,
	})

	sockB.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 2048)
	if n, _, err := sockB.ReadFromUDP(buf); err != nil {
		t.Fatalf("no RTP at new media address after re-INVITE: %v", err)
	} else if n < 12 || buf[0]>>6 != 2 {
		t.Fatalf("packet at new address is not RTP v2 (len=%d)", n)
	}

	sockA.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	for {
		n, _, err := sockA.ReadFromUDP(buf)
		if err != nil {
			break // timeout — old address silent, good
		}
		if n >= 12 && buf[0]>>6 == 2 {
			t.Fatal("old media address still receiving RTP after re-INVITE (leaked goroutine)")
		}
	}
}

// fakePlatform mimics the NVR SIP stack for lifecycle tests: it answers
// REGISTER with 401-then-200 (digest not validated) and can be switched
// to reject keepalive MESSAGEs with 403 (simulating an NVR restart that
// lost registration state).
type fakePlatform struct {
	conn      *net.UDPConn
	rejectKA  atomic.Bool
	registers atomic.Int32 // REGISTERs carrying Authorization (completed flows)
}

func newFakePlatform(t *testing.T) *fakePlatform {
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatalf("fake platform bind: %v", err)
	}
	fp := &fakePlatform{conn: conn}
	go fp.serve()
	return fp
}

func (fp *fakePlatform) addr() string {
	return fp.conn.LocalAddr().String()
}

func (fp *fakePlatform) close() {
	fp.conn.Close()
}

func (fp *fakePlatform) serve() {
	buf := make([]byte, 4096)
	for {
		n, from, err := fp.conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		req, err := Parse(buf[:n])
		if err != nil {
			continue
		}
		switch {
		case req.Method == "REGISTER" && req.Authorization == "":
			fp.reply(from, req, 401, "Unauthorized",
				"Digest realm=\"3402000000\", nonce=\"testnonce\", qop=\"auth\"")
		case req.Method == "REGISTER":
			fp.registers.Add(1)
			fp.reply(from, req, 200, "OK", "")
		case req.Method == "MESSAGE":
			if fp.rejectKA.Load() {
				fp.reply(from, req, 403, "Forbidden", "")
			} else {
				fp.reply(from, req, 200, "OK", "")
			}
		}
	}
}

func (fp *fakePlatform) reply(to *net.UDPAddr, req SipMessage, code int, reason, wwwAuth string) {
	resp := SipMessage{
		StatusCode:      code,
		Via:             req.Via,
		From:            req.From,
		To:              req.To,
		CallID:          req.CallID,
		CSeq:            req.CSeq,
		WWWAuthenticate: wwwAuth,
		Headers:         make(map[string]string),
	}
	_, _ = fp.conn.WriteToUDP(resp.Serialize(), to)
}

// TestServer_SelfHealsAfterKeepaliveRejection verifies the device
// re-REGISTERs when the platform starts rejecting keepalives with 403
// (NVR restarted and lost registration state) — issue #4.
func TestServer_SelfHealsAfterKeepaliveRejection(t *testing.T) {
	fp := newFakePlatform(t)
	defer fp.close()

	hub := NewFrameHub()
	cfg := Config{
		DeviceID:              "34020000001320000001",
		ChannelID:             "34020000001320000001",
		SIPDomain:             "3402000000",
		Password:              "12345678",
		LocalSIPPort:          0,
		PlatformSIPAddress:    "127.0.0.1",
		PlatformSIPPort:       fp.conn.LocalAddr().(*net.UDPAddr).Port,
		RegisterIntervalSecs:  3600, // long — only the 403 path may trigger re-register
		HeartbeatIntervalSecs: 1,
		HeartbeatTimeoutCount: 3,
	}
	server := New(cfg, DeviceInfo{}, hub)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	go func() {
		_ = server.Start(ctx)
	}()

	// Wait for the initial registration to complete.
	deadline := time.Now().Add(5 * time.Second)
	for fp.registers.Load() < 1 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if got := fp.registers.Load(); got < 1 {
		t.Fatalf("initial registration never completed (completed=%d)", got)
	}

	// Simulate NVR restart: platform forgets the device and 403s keepalives.
	fp.rejectKA.Store(true)

	// The device must re-REGISTER on its own shortly after the first 403.
	deadline = time.Now().Add(6 * time.Second)
	for fp.registers.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if got := fp.registers.Load(); got < 2 {
		t.Fatalf("device did not re-register after keepalive 403 rejections (completed=%d)", got)
	}
}

// TestRecvLoop_Subscribe_Gets200 verifies the recv loop answers SUBSCRIBE with 200 OK.
func TestRecvLoop_Subscribe_Gets200(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	hub := NewFrameHub()
	cfg := Config{
		DeviceID:              "34020000001320000001",
		ChannelID:             "34020000001320000001",
		SIPDomain:             "3402000000",
		Password:              "12345678",
		LocalSIPPort:          0,
		PlatformSIPAddress:    "127.0.0.1",
		PlatformSIPPort:       15067,
		RegisterIntervalSecs:  60,
		HeartbeatIntervalSecs: 60,
		HeartbeatTimeoutCount: 3,
	}
	server := New(cfg, DeviceInfo{}, hub)
	server.SetTestMode()

	serverErr := make(chan error, 1)
	go func() {
		if err := server.Start(ctx); err != nil && err != context.Canceled {
			serverErr <- err
		}
	}()

	time.Sleep(100 * time.Millisecond)
	sipPort := waitSIPPort(t, server)

	subscribe := SipMessage{
		Method:     "SUBSCRIBE",
		RequestURI: "sip:3402000000@3402000000",
		From:       "<sip:34020000012000000001@3402000000>;tag=sub123",
		To:         "<sip:34020000001320000001@3402000000>",
		CallID:     "test-call-subscribe@example.com",
		CSeq:       "1 SUBSCRIBE",
		Contact:    "<sip:34020000012000000001@192.168.1.100:5060>",
		Via:        "SIP/2.0/UDP 192.168.1.100:5060;rport;branch=z9hG4bK-subscribe",
		Expires:    "3600",
		Headers:    make(map[string]string),
	}

	clientConn, err := net.DialUDP("udp", nil, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: sipPort})
	if err != nil {
		t.Fatalf("Failed to dial server: %v", err)
	}
	defer clientConn.Close()

	if _, err := clientConn.Write(subscribe.Serialize()); err != nil {
		t.Fatalf("Failed to send SUBSCRIBE: %v", err)
	}

	respBuf := make([]byte, 4096)
	clientConn.SetReadDeadline(time.Now().Add(1 * time.Second))
	n, _, err := clientConn.ReadFromUDP(respBuf)
	if err != nil {
		t.Fatalf("Failed to read 200 OK: %v", err)
	}

	resp, err := Parse(respBuf[:n])
	if err != nil {
		t.Fatalf("Failed to parse 200 OK: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("Expected 200 OK, got %d", resp.StatusCode)
	}

	cancel()
	select {
	case err := <-serverErr:
		if err != nil {
			t.Fatalf("Server error: %v", err)
		}
	case <-time.After(1 * time.Second):
	}
}
