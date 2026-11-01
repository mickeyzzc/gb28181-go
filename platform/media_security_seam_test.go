package platform

import (
	"testing"
)

// The GB35114 media-security seams are tag-free so default builds carry
// zero national-cryptography code; these default-build tests pin the
// unarmed (plaintext) contract of each seam.

func TestReceiverMediaSecuritySeamsUnarmedByDefault(t *testing.T) {
	hub := NewFrameHub()
	pm := NewPortManager(31000, 31010)
	rec := NewReceiver("seam-cam", hub, pm)

	// Fresh receiver: nothing armed, nothing failed.
	if dec, ver := rec.MediaSecurityArmed(); dec || ver {
		t.Fatalf("fresh receiver must be unarmed, got decrypt=%v verify=%v", dec, ver)
	}
	if rec.AUVerifyFailures() != 0 {
		t.Fatalf("fresh receiver AUVerifyFailures = %d, want 0", rec.AUVerifyFailures())
	}

	// Arming the setters flips the reporters (the hooks themselves are
	// tag-free interfaces — fakes carry no crypto).
	rec.SetPayloadDecryptor(fakeCryptor{})
	rec.SetAUVerifier(func([]byte, uint32, uint32, []byte) error { return nil })
	if dec, ver := rec.MediaSecurityArmed(); !dec || !ver {
		t.Fatalf("armed receiver must report both hooks, got decrypt=%v verify=%v", dec, ver)
	}

	// AudioOnly is a hint, not a security state: default false.
	if rec.AudioOnly() {
		t.Fatal("fresh receiver must not report AudioOnly")
	}
}

// fakeCryptor implements RTPCryptor without cryptography: identity pass
// through (the XOR construction makes encrypt and decrypt the same op, so
// an identity cryptor models "cryptor that changes nothing").
type fakeCryptor struct{}

func (fakeCryptor) Crypt(p []byte, _ uint32, _ uint16) []byte { return p }
func (fakeCryptor) Zero()                                     {}

func TestSessionManagerMediaSecurityOfferSeam(t *testing.T) {
	sm := NewSessionManager(NewPortManager(31100, 31110), "34020000002000000001")

	// No resolver installed: offers carry no a=security value.
	if got := sm.mediaSecurityOffer(); got != "" {
		t.Fatalf("mediaSecurityOffer with nil resolver = %q, want empty", got)
	}
	sm.SetMediaSecurityOffer(func() string { return "1" })
	if got := sm.mediaSecurityOffer(); got != "1" {
		t.Fatalf("mediaSecurityOffer with resolver = %q, want 1", got)
	}
	// The empty resolver is the documented "disable" value.
	sm.SetMediaSecurityOffer(func() string { return "" })
	if got := sm.mediaSecurityOffer(); got != "" {
		t.Fatalf("mediaSecurityOffer with empty resolver = %q, want empty", got)
	}
}

func TestSessionManagerPlaybackActive(t *testing.T) {
	sm := NewSessionManager(NewPortManager(31200, 31210), "34020000002000000001")
	if sm.PlaybackActive("ch") {
		t.Fatal("no session registered: PlaybackActive must be false")
	}
	sm.playbackSessions["ch"] = &session{}
	if !sm.PlaybackActive("ch") {
		t.Fatal("registered session must report PlaybackActive")
	}
}
