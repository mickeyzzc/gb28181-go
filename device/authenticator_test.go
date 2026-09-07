// Seam tests for the RegisterAuthenticator/OutgoingSigner hooks: a stub
// implementation exercises the lifecycle wiring without any GB35114
// dependency (the tagged integration test lives in
// authenticator_35114_test.go).

package device

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"
)

var errFakeVerify = errors.New("fake verify failure")

// stubAuthenticator records the calls the lifecycle makes. The lifecycle
// runs on its own goroutine, so recordings are mutex-guarded for the
// -race detector (UDP round-trips are no happens-before edge).
type stubAuthenticator struct {
	initial   string
	challenge string
	okE       error
	date      string
	note      string

	mu           sync.Mutex
	gotChallenge string
	gotOK        string
	calls        int
}

func (s *stubAuthenticator) InitialAuthorization() string { return s.initial }

func (s *stubAuthenticator) AuthorizeWithChallenge(wwwAuthenticate string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gotChallenge = wwwAuthenticate
	return s.challenge, nil
}

func (s *stubAuthenticator) VerifyOK(securityInfo string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gotOK = securityInfo
	return s.okE
}

// DecorateOutgoing only decorates non-REGISTER methods, like the real
// GB35114 signer.
func (s *stubAuthenticator) DecorateOutgoing(method, from, to, callID, body string) (string, string) {
	if method == "REGISTER" {
		return "", ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	return s.date, s.note
}

func (s *stubAuthenticator) snapshot() (gotChallenge, gotOK string, calls int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.gotChallenge, s.gotOK, s.calls
}

func newSeamTestServer(t *testing.T, hook RegisterAuthenticator, platform *net.UDPConn) *Server {
	t.Helper()
	cfg := Config{
		DeviceID:              "34020000001320000001",
		ChannelID:             "34020000001320000001",
		SIPDomain:             "3402000000",
		Password:              "12345678",
		LocalSIPPort:          0,
		PlatformSIPAddress:    "127.0.0.1",
		PlatformSIPPort:       platform.LocalAddr().(*net.UDPAddr).Port,
		RegisterIntervalSecs:  3600,
		HeartbeatIntervalSecs: 3600,
		HeartbeatTimeoutCount: 3,
		RegisterAuthenticator: hook,
	}
	srv := New(cfg, DeviceInfo{}, NewFrameHub())
	srv.SetTestMode()
	// Bind the SIP socket the way Start() would so the lifecycle can send
	// without running the full server loop.
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatalf("binding SIP socket: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	srv.sipConn = conn
	return srv
}

// readSIP reads one SIP datagram from conn, returning the parsed message
// and the sender address.
func readSIP(t *testing.T, conn *net.UDPConn) (SipMessage, *net.UDPAddr) {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, 65535)
	n, peer, err := conn.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("fake platform read: %v", err)
	}
	msg, err := Parse(buf[:n])
	if err != nil {
		t.Fatalf("fake platform parse: %v", err)
	}
	return msg, peer
}

func writeSIP(t *testing.T, conn *net.UDPConn, msg SipMessage, peer *net.UDPAddr) {
	t.Helper()
	if _, err := conn.WriteToUDP(msg.Serialize(), peer); err != nil {
		t.Fatalf("fake platform write: %v", err)
	}
}

func TestRegisterLifecycleWithAuthenticatorHook(t *testing.T) {
	platConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer platConn.Close()

	stub := &stubAuthenticator{
		initial:   `Capability algorithm="A:SM2;H:SM3", keyversion="t"`,
		challenge: `Unidirection random1="r1", random2="r2", sign1="s1"`,
	}
	srv := newSeamTestServer(t, stub, platConn)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- srv.runRegisterLifecycleWith(ctx, srv.socketResponseSource()) }()

	// 1st REGISTER: carries the hook's initial authorization.
	reg1, peer := readSIP(t, platConn)
	if reg1.Authorization != stub.initial {
		t.Fatalf("initial REGISTER Authorization = %q, want %q", reg1.Authorization, stub.initial)
	}

	// 401 with the GB35114 challenge.
	challenge := SipMessage{
		StatusCode:      401,
		Via:             reg1.Via,
		From:            reg1.From,
		To:              reg1.To + ";tag=fake",
		CallID:          reg1.CallID,
		CSeq:            reg1.CSeq,
		WWWAuthenticate: `Unidirection algorithm="A:SM2;H:SM3", random1="r1"`,
		Headers:         make(map[string]string),
	}
	writeSIP(t, platConn, challenge, peer)

	// 2nd REGISTER: hook-produced authorization.
	reg2, _ := readSIP(t, platConn)
	if reg2.Authorization != stub.challenge {
		t.Fatalf("auth REGISTER Authorization = %q, want %q", reg2.Authorization, stub.challenge)
	}
	gotChallenge, _, _ := stub.snapshot()
	if gotChallenge != challenge.WWWAuthenticate {
		t.Fatalf("hook saw challenge %q", gotChallenge)
	}

	// 200 OK with SecurityInfo.
	ok := SipMessage{
		StatusCode: 200,
		Via:        reg2.Via,
		From:       reg2.From,
		To:         reg2.To,
		CallID:     reg2.CallID,
		CSeq:       reg2.CSeq,
		Headers:    map[string]string{"SecurityInfo": `Unidirection cryptkey="ck", algorithm="A:SM2;H:SM3"`},
	}
	writeSIP(t, platConn, ok, peer)

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("lifecycle: %v", err)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("lifecycle did not finish")
	}
	_, gotOK, _ := stub.snapshot()
	if gotOK != `Unidirection cryptkey="ck", algorithm="A:SM2;H:SM3"` {
		t.Fatalf("hook saw SecurityInfo %q", gotOK)
	}
}

func TestRegisterLifecycleHookErrorPropagates(t *testing.T) {
	platConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer platConn.Close()

	stub := &stubAuthenticator{okE: errFakeVerify}
	srv := newSeamTestServer(t, stub, platConn)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- srv.runRegisterLifecycleWith(ctx, srv.socketResponseSource()) }()

	reg1, peer := readSIP(t, platConn)
	unauth := SipMessage{
		StatusCode:      401,
		Via:             reg1.Via,
		From:            reg1.From,
		To:              reg1.To + ";tag=fake",
		CallID:          reg1.CallID,
		CSeq:            reg1.CSeq,
		WWWAuthenticate: `Unidirection algorithm="A:SM2;H:SM3", random1="r1"`,
		Headers:         make(map[string]string),
	}
	writeSIP(t, platConn, unauth, peer)

	reg2, _ := readSIP(t, platConn)
	ok := SipMessage{
		StatusCode: 200,
		Via:        reg2.Via,
		From:       reg2.From,
		To:         reg2.To,
		CallID:     reg2.CallID,
		CSeq:       reg2.CSeq,
		Headers:    map[string]string{"SecurityInfo": `Unidirection cryptkey="ck"`},
	}
	writeSIP(t, platConn, ok, peer)

	select {
	case err := <-errCh:
		if err == nil || !errors.Is(err, errFakeVerify) {
			t.Fatalf("lifecycle error = %v, want fake verify failure", err)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("lifecycle did not finish")
	}
}

func TestSendToPlatformDecoratesNonRegister(t *testing.T) {
	platConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer platConn.Close()

	stub := &stubAuthenticator{date: "2026-01-01T00:00:00.000", note: `Digest nonce="n==",algorithm=SM3`}
	srv := newSeamTestServer(t, stub, platConn)
	platformAddr := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: platConn.LocalAddr().(*net.UDPAddr).Port}

	// Keepalive MESSAGE gets Date + Note.
	msg := BuildKeepaliveMessage("1", "34020000001320000001", "OK")
	msg.Via = "SIP/2.0/UDP 127.0.0.1:5060;branch=z9hG4bKtest"
	if err := srv.sendToPlatform(msg, platformAddr); err != nil {
		t.Fatalf("sendToPlatform: %v", err)
	}
	sent, _ := readSIP(t, platConn)
	if got := sent.ExtensionHeader("Date"); got != stub.date {
		t.Fatalf("Date = %q, want %q", got, stub.date)
	}
	if got := sent.ExtensionHeader("Note"); got != stub.note {
		t.Fatalf("Note = %q, want %q", got, stub.note)
	}
	if _, _, calls := stub.snapshot(); calls != 1 {
		t.Fatalf("signer calls = %d, want 1", calls)
	}

	// REGISTER is never decorated.
	regMsg := BuildRegister("sip:3402000000@3402000000", "<sip:d@3402000000>", "<sip:s@3402000000>", "c@h", "1 REGISTER", "", "")
	if err := srv.sendToPlatform(regMsg, platformAddr); err != nil {
		t.Fatalf("sendToPlatform REGISTER: %v", err)
	}
	sentReg, _ := readSIP(t, platConn)
	if got := sentReg.ExtensionHeader("Note"); got != "" {
		t.Fatalf("REGISTER carried Note %q", got)
	}
	if _, _, calls := stub.snapshot(); calls != 1 {
		t.Fatalf("signer calls after REGISTER = %d, want 1", calls)
	}
}
