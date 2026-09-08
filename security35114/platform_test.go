//go:build gb35114

package security35114

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/emmansun/gmsm/smx509"
)

// newTestAuthenticator builds the device side of loopback tests with the
// fixed rand so random2 (and therefore sign1) is deterministic.
func newLoopbackDevice(t *testing.T, dev *Identity, platCert *smx509.Certificate, serverID string) *Authenticator {
	t.Helper()
	a, err := New(Options{
		Device:       dev,
		PlatformCert: platCert,
		DeviceID:     goldenDevice,
		ServerID:     serverID,
		KeyVersion:   "2026-01-01T00:00:00.000",
		Rand:         fixedRand{},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return a
}

// newTestPlatform builds the platform side with the fixed rand so random1,
// the VKEK, and the SM2 encryption ephemeral are deterministic.
func newTestPlatform(t *testing.T, devCert *smx509.Certificate) *Platform {
	t.Helper()
	p, err := NewPlatform(PlatformConfig{
		ServerID:    goldenServer,
		Identity:    loadPlatformIdentity(t),
		DeviceCerts: map[string]*smx509.Certificate{goldenDevice: devCert},
		Rand:        fixedRand{},
	})
	if err != nil {
		t.Fatalf("NewPlatform: %v", err)
	}
	return p
}

// counterRand yields a different byte pattern per Read so successive
// challenges (random1) differ while staying deterministic — fixedRand
// draws the same value every time, which would mask replay detection.
type counterRand struct{ n byte } // pre-increment: first draw yields 0x02

func (c *counterRand) Read(p []byte) (int, error) {
	c.n++
	for i := range p {
		p[i] = c.n
	}
	return len(p), nil
}

func TestPlatformLoopbackBidirection(t *testing.T) {
	dev := loadDeviceIdentity(t)
	plat := loadPlatformIdentity(t)
	a := newLoopbackDevice(t, dev, plat.Certificate, goldenServer)
	p := newTestPlatform(t, dev.Certificate)

	wwwAuth, err := p.Challenge(goldenDevice, a.InitialAuthorization())
	if err != nil {
		t.Fatalf("Challenge: %v", err)
	}
	if !strings.HasPrefix(wwwAuth, `Bidirection algorithm="A:SM2;H:SM3", random1="`) {
		t.Fatalf("challenge = %q", wwwAuth)
	}

	auth, err := a.AuthorizeWithChallenge(wwwAuth)
	if err != nil {
		t.Fatalf("AuthorizeWithChallenge: %v", err)
	}
	si, err := p.VerifyRegister(goldenDevice, auth)
	if err != nil {
		t.Fatalf("VerifyRegister: %v", err)
	}
	if err := a.VerifyOK(si); err != nil {
		t.Fatalf("device rejected SecurityInfo: %v", err)
	}
	if !bytes.Equal(a.VKEK(), p.VKEK(goldenDevice)) {
		t.Fatalf("VKEK mismatch: device % x, platform % x", a.VKEK(), p.VKEK(goldenDevice))
	}

	// Note roundtrip: the device signs a keepalive, the platform verifies.
	from, to, callID, body := "<sip:"+goldenDevice+"@3402000000>", "<sip:3402000000@3402000000>", "1@dev", "keepalive"
	date, note := a.DecorateOutgoing("MESSAGE", from, to, callID, body)
	if note == "" {
		t.Fatalf("no Note header after handshake")
	}
	if err := p.VerifyNote(goldenDevice, note, "MESSAGE", from, to, callID, date, body); err != nil {
		t.Fatalf("VerifyNote: %v", err)
	}
	if err := p.VerifyNote(goldenDevice, note, "MESSAGE", from, to, callID, date, body+"x"); err == nil {
		t.Fatalf("tampered body accepted")
	}
	if err := p.VerifyNote(goldenDevice, "", "MESSAGE", from, to, callID, date, body); err != nil {
		t.Fatalf("empty Note should be a no-op pass-through, got %v", err)
	}
}

func TestPlatformLoopbackUnidirection(t *testing.T) {
	dev := loadDeviceIdentity(t)
	a := newLoopbackDevice(t, dev, nil, goldenServer)

	// No Identity: the platform challenges Unidirection and signs nothing.
	p, err := NewPlatform(PlatformConfig{
		ServerID:    goldenServer,
		DeviceCerts: map[string]*smx509.Certificate{goldenDevice: dev.Certificate},
		Rand:        fixedRand{},
	})
	if err != nil {
		t.Fatalf("NewPlatform: %v", err)
	}

	wwwAuth, err := p.Challenge(goldenDevice, a.InitialAuthorization())
	if err != nil {
		t.Fatalf("Challenge: %v", err)
	}
	if !strings.HasPrefix(wwwAuth, "Unidirection ") {
		t.Fatalf("challenge = %q", wwwAuth)
	}
	auth, err := a.AuthorizeWithChallenge(wwwAuth)
	if err != nil {
		t.Fatalf("AuthorizeWithChallenge: %v", err)
	}
	si, err := p.VerifyRegister(goldenDevice, auth)
	if err != nil {
		t.Fatalf("VerifyRegister: %v", err)
	}
	if err := a.VerifyOK(si); err != nil {
		t.Fatalf("device rejected SecurityInfo: %v", err)
	}
	if !bytes.Equal(a.VKEK(), p.VKEK(goldenDevice)) {
		t.Fatalf("VKEK mismatch")
	}
}

func TestPlatformLoopbackCnonceCert(t *testing.T) {
	// Device announces its certificate via cnonce; the platform has no
	// pre-provisioned certs at all.
	dev := loadDeviceIdentity(t)
	plat := loadPlatformIdentity(t)
	a, err := New(Options{
		Device:            dev,
		PlatformCert:      plat.Certificate,
		DeviceID:          goldenDevice,
		ServerID:          goldenServer,
		KeyVersion:        "2026-01-01T00:00:00.000",
		IncludeDeviceCert: true,
		Rand:              fixedRand{},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	p, err := NewPlatform(PlatformConfig{ServerID: goldenServer, Rand: fixedRand{}})
	if err != nil {
		t.Fatalf("NewPlatform: %v", err)
	}

	wwwAuth, err := p.Challenge(goldenDevice, a.InitialAuthorization())
	if err != nil {
		t.Fatalf("Challenge: %v", err)
	}
	auth, err := a.AuthorizeWithChallenge(wwwAuth)
	if err != nil {
		t.Fatalf("AuthorizeWithChallenge: %v", err)
	}
	if _, err := p.VerifyRegister(goldenDevice, auth); err != nil {
		t.Fatalf("VerifyRegister with cnonce-announced cert: %v", err)
	}
}

func TestPlatformIdempotentRetransmission(t *testing.T) {
	// SIP-over-UDP retransmits the same REGISTER until the 200 OK lands;
	// the second VerifyRegister must return the same SecurityInfo.
	dev := loadDeviceIdentity(t)
	a := newLoopbackDevice(t, dev, loadPlatformCert(t), goldenServer)
	p := newTestPlatform(t, dev.Certificate)

	wwwAuth, _ := p.Challenge(goldenDevice, a.InitialAuthorization())
	auth, _ := a.AuthorizeWithChallenge(wwwAuth)
	si1, err := p.VerifyRegister(goldenDevice, auth)
	if err != nil {
		t.Fatalf("first VerifyRegister: %v", err)
	}
	si2, err := p.VerifyRegister(goldenDevice, auth)
	if err != nil {
		t.Fatalf("retransmitted VerifyRegister: %v", err)
	}
	if si1 != si2 {
		t.Fatalf("retransmission produced a different SecurityInfo")
	}
}

func TestPlatformRejectsStaleRandom1(t *testing.T) {
	// Answering a NEW challenge with the OLD handshake's Authorization is
	// a replay; the mismatched random1 must be rejected.
	dev := loadDeviceIdentity(t)
	a := newLoopbackDevice(t, dev, loadPlatformCert(t), goldenServer)
	p := newTestPlatform(t, dev.Certificate)

	wwwAuth1, _ := p.Challenge(goldenDevice, a.InitialAuthorization())
	auth1, _ := a.AuthorizeWithChallenge(wwwAuth1)

	// Re-challenge with a fresh (different) random1: the platform must
	// refuse the Authorization that answers the old challenge.
	stale, err := NewPlatform(PlatformConfig{
		ServerID:    goldenServer,
		Identity:    loadPlatformIdentity(t),
		DeviceCerts: map[string]*smx509.Certificate{goldenDevice: dev.Certificate},
		Rand:        &counterRand{n: 1},
	})
	if err != nil {
		t.Fatalf("NewPlatform: %v", err)
	}
	if _, err := stale.Challenge(goldenDevice, a.InitialAuthorization()); err != nil {
		t.Fatalf("Challenge: %v", err)
	}
	_, err = stale.VerifyRegister(goldenDevice, auth1)
	if !errors.Is(err, ErrChallengeMismatch) {
		t.Fatalf("stale random1: err = %v, want ErrChallengeMismatch", err)
	}
}

func TestPlatformRejectsUnknownCert(t *testing.T) {
	dev := loadDeviceIdentity(t)
	a := newLoopbackDevice(t, dev, loadPlatformCert(t), goldenServer)
	p, err := NewPlatform(PlatformConfig{ServerID: goldenServer, Rand: fixedRand{}})
	if err != nil {
		t.Fatalf("NewPlatform: %v", err)
	}

	wwwAuth, _ := p.Challenge(goldenDevice, a.InitialAuthorization()) // no cnonce announced
	auth, _ := a.AuthorizeWithChallenge(wwwAuth)
	_, err = p.VerifyRegister(goldenDevice, auth)
	if !errors.Is(err, ErrDeviceCert) {
		t.Fatalf("unknown device: err = %v, want ErrDeviceCert", err)
	}
}

func TestPlatformRejectsBadSign1(t *testing.T) {
	// The device signs with its own key but the platform trusts a
	// different certificate for that device ID → sign1 must not verify.
	dev := loadDeviceIdentity(t)
	a := newLoopbackDevice(t, dev, loadPlatformCert(t), goldenServer)
	p := newTestPlatform(t, loadPlatformCert(t)) // wrong cert for goldenDevice

	wwwAuth, _ := p.Challenge(goldenDevice, a.InitialAuthorization())
	auth, _ := a.AuthorizeWithChallenge(wwwAuth)
	_, err := p.VerifyRegister(goldenDevice, auth)
	if err == nil || !strings.Contains(err.Error(), "sign1") {
		t.Fatalf("forged sign1: err = %v, want sign1 verification failure", err)
	}
}

func TestPlatformRejectsServerIDMismatch(t *testing.T) {
	// The device believes it talks to a different platform: its sign1
	// covers that other server ID, so this platform must refuse.
	dev := loadDeviceIdentity(t)
	a := newLoopbackDevice(t, dev, loadPlatformCert(t), "34020000002000000099")
	p := newTestPlatform(t, dev.Certificate)

	wwwAuth, _ := p.Challenge(goldenDevice, a.InitialAuthorization())
	auth, _ := a.AuthorizeWithChallenge(wwwAuth)
	_, err := p.VerifyRegister(goldenDevice, auth)
	if !errors.Is(err, ErrServerIDMismatch) {
		t.Fatalf("server ID mismatch: err = %v, want ErrServerIDMismatch", err)
	}
}

func TestPlatformVerifyRegisterWithoutChallenge(t *testing.T) {
	dev := loadDeviceIdentity(t)
	p := newTestPlatform(t, dev.Certificate)

	auth := BuildAuthAuthorization(
		Challenge{Mode: ModeBidirection, Algorithm: "A:SM2;H:SM3", Random1: goldenRandom1},
		goldenRandom2, goldenServer, goldenDevice, goldenSign1)
	_, err := p.VerifyRegister(goldenDevice, auth)
	if !errors.Is(err, ErrNoChallenge) {
		t.Fatalf("err = %v, want ErrNoChallenge", err)
	}
}

func TestPlatformNoteBeforeHandshake(t *testing.T) {
	dev := loadDeviceIdentity(t)
	p := newTestPlatform(t, dev.Certificate)
	err := p.VerifyNote(goldenDevice, "Digest nonce=\"x\",algorithm=SM3", "MESSAGE", "f", "t", "c", goldenDate, "")
	if err == nil {
		t.Fatalf("VerifyNote before handshake must fail")
	}
}

func TestPlatformChallengeKeepsActiveVKEK(t *testing.T) {
	// A re-REGISTER starts a new challenge; until it completes, keepalives
	// keyed with the previous VKEK must keep verifying.
	dev := loadDeviceIdentity(t)
	a := newLoopbackDevice(t, dev, loadPlatformCert(t), goldenServer)
	p := newTestPlatform(t, dev.Certificate)

	wwwAuth1, _ := p.Challenge(goldenDevice, a.InitialAuthorization())
	auth1, _ := a.AuthorizeWithChallenge(wwwAuth1)
	si1, _ := p.VerifyRegister(goldenDevice, auth1)
	if err := a.VerifyOK(si1); err != nil {
		t.Fatalf("VerifyOK: %v", err)
	}
	from, to, callID, body := "<sip:d@3402000000>", "<sip:s@3402000000>", "1@dev", "keepalive"
	date, note := a.DecorateOutgoing("MESSAGE", from, to, callID, body)

	if _, err := p.Challenge(goldenDevice, a.InitialAuthorization()); err != nil {
		t.Fatalf("re-challenge: %v", err)
	}
	if err := p.VerifyNote(goldenDevice, note, "MESSAGE", from, to, callID, date, body); err != nil {
		t.Fatalf("keepalive during re-registration must still verify: %v", err)
	}
}
