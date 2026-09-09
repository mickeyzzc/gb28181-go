package device_test

// Device-side incoming Note verification (issue #52): when the
// RegisterAuthenticator implements IncomingNoteVerifier, platform→device
// requests are checked before dispatch. Default policy rejects a bad
// Note with 403; Warn lets the request through with a log line; a good
// or absent Note always passes (mixed-mode Digest platforms).

import (
	"context"
	"errors"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/mickeyzzc/gb28181-go/device"
)

// noteStubAuth accepts the REGISTER flow silently and verifies incoming
// Notes with one rule: note == "good" passes, anything else fails.
type noteStubAuth struct{}

func (noteStubAuth) InitialAuthorization() string { return "" }

func (noteStubAuth) AuthorizeWithChallenge(string) (string, error) {
	return "", nil
}

func (noteStubAuth) VerifyOK(string) error { return nil }

func (noteStubAuth) VerifyIncomingNote(method, from, to, callID, date, note, body string) error {
	if note == "" {
		return nil // mixed-mode tolerance, mirrors the platform side
	}
	if note == "good" {
		return nil
	}
	return errors.New("stub: bad note")
}

// startNoteTestServer boots a full device.Server against a fake platform
// socket, completes the REGISTER lifecycle with a bare 200 OK, and
// returns a sender that writes platform requests plus a reader that
// skips lifecycle noise.
func startNoteTestServer(t *testing.T, policy device.GB35114NotePolicy) (*net.UDPConn, *net.UDPAddr) {
	t.Helper()

	probe, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	sipPort := probe.LocalAddr().(*net.UDPAddr).Port
	probe.Close()

	platConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatalf("platform listen: %v", err)
	}
	t.Cleanup(func() { _ = platConn.Close() })

	cfg := device.Config{
		DeviceID:              "34020000001320000001",
		ChannelID:             "34020000001320000001",
		SIPDomain:             "3402000000",
		Password:              "12345678",
		LocalSIPPort:          sipPort,
		PlatformSIPAddress:    "127.0.0.1",
		PlatformSIPPort:       platConn.LocalAddr().(*net.UDPAddr).Port,
		RegisterIntervalSecs:  3600,
		HeartbeatIntervalSecs: 3600,
		HeartbeatTimeoutCount: 3,
		RegisterAuthenticator: noteStubAuth{},
		IncomingNotePolicy:    policy,
	}
	srv := device.New(cfg, device.DeviceInfo{}, device.NewFrameHub())

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.Cleanup(cancel)
	go func() { _ = srv.Start(ctx) }()

	// Quiet the register lifecycle: answer the first REGISTER with a bare
	// 200 OK (no WWW-Authenticate — the stub authenticator is done).
	reg, peer := readNoteMsg(t, platConn)
	if reg.Method != "REGISTER" {
		t.Fatalf("first message from device = %q, want REGISTER", reg.Method)
	}
	writeNoteMsg(t, platConn, device.SipMessage{
		StatusCode: 200,
		Via:        reg.Via,
		From:       reg.From,
		To:         reg.To,
		CallID:     reg.CallID,
		CSeq:       reg.CSeq,
		Headers:    map[string]string{},
	}, peer)

	return platConn, peer
}

func readNoteMsg(t *testing.T, conn *net.UDPConn) (device.SipMessage, *net.UDPAddr) {
	t.Helper()

	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, 65535)
	n, peer, err := conn.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("fake platform read: %v", err)
	}
	msg, err := device.Parse(buf[:n])
	if err != nil {
		t.Fatalf("fake platform parse: %v", err)
	}
	return msg, peer
}

func writeNoteMsg(t *testing.T, conn *net.UDPConn, msg device.SipMessage, peer *net.UDPAddr) {
	t.Helper()

	if _, err := conn.WriteToUDP(msg.Serialize(), peer); err != nil {
		t.Fatalf("fake platform write: %v", err)
	}
}

// platformOptions builds an OPTIONS request carrying optional Note/Date
// headers.
func platformOptions(callID, note, date string) device.SipMessage {
	headers := map[string]string{}
	if note != "" {
		headers["Note"] = note
	}
	if date != "" {
		headers["Date"] = date
	}
	return device.SipMessage{
		Method:      "OPTIONS",
		RequestURI:  "sip:34020000001320000001@3402000000",
		From:        "<sip:34020000002000000001@3402000000>;tag=plat" + strconv.FormatInt(time.Now().UnixNano(), 36),
		To:          "<sip:34020000001320000001@3402000000>",
		CallID:      callID,
		CSeq:        "1 OPTIONS",
		Via:         "SIP/2.0/UDP 127.0.0.1:5060;branch=z9hG4bKnote" + callID,
		ContentType: "",
		Body:        "",
		UserAgent:   "fakeplatform",
		Headers:     headers,
	}
}

func TestIncomingNoteRejectPolicyAnswers403OnBadNote(t *testing.T) {
	platConn, peer := startNoteTestServer(t, device.GB35114NoteReject) // default policy

	writeNoteMsg(t, platConn, platformOptions("note-r1", "garbage", "2026-09-09T00:00:00.000"), peer)

	resp, _ := readNoteMsg(t, platConn)
	if resp.StatusCode != 403 {
		t.Fatalf("status = %d, want 403 for a bad Note under the reject policy", resp.StatusCode)
	}
}

func TestIncomingNoteRejectPolicyAcceptsGoodNote(t *testing.T) {
	platConn, peer := startNoteTestServer(t, device.GB35114NoteReject)

	writeNoteMsg(t, platConn, platformOptions("note-r2", "good", "2026-09-09T00:00:00.000"), peer)

	resp, _ := readNoteMsg(t, platConn)
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200 for a valid Note", resp.StatusCode)
	}
}

func TestIncomingNoteAbsentNotePasses(t *testing.T) {
	platConn, peer := startNoteTestServer(t, device.GB35114NoteReject)

	writeNoteMsg(t, platConn, platformOptions("note-r3", "", ""), peer)

	resp, _ := readNoteMsg(t, platConn)
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200 — a Note-less request passes (mixed-mode Digest platforms)", resp.StatusCode)
	}
}

func TestIncomingNoteWarnPolicyLogsButServes(t *testing.T) {
	platConn, peer := startNoteTestServer(t, device.GB35114NoteWarn)

	writeNoteMsg(t, platConn, platformOptions("note-w1", "garbage", "2026-09-09T00:00:00.000"), peer)

	resp, _ := readNoteMsg(t, platConn)
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200 under the warn policy", resp.StatusCode)
	}
}

func TestIncomingNoteOffPolicySkipsVerification(t *testing.T) {
	platConn, peer := startNoteTestServer(t, device.GB35114NoteOff)

	writeNoteMsg(t, platConn, platformOptions("note-o1", "garbage", "2026-09-09T00:00:00.000"), peer)

	resp, _ := readNoteMsg(t, platConn)
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200 with verification off", resp.StatusCode)
	}
}
