//go:build gb35114

package security35114

// VerifyIncomingNote (issue #52 / device.IncomingNoteVerifier): after the
// A-level handshake, platform→device requests carry a Note the device
// verifies against its VKEK. No Note passes (mixed-mode Digest
// platforms); tampered payloads, stale Dates, and pre-handshake
// verification fail.

import (
	"bytes"
	"testing"
	"time"
)

// establishedAuthenticator runs the full Unidirection dance so the
// authenticator holds goldenVKEK.
func establishedAuthenticator(t *testing.T) *Authenticator {
	t.Helper()

	a, err := New(Options{
		Device:     loadDeviceIdentity(t),
		DeviceID:   goldenDevice,
		ServerID:   goldenServer,
		KeyVersion: "2026-01-01T00:00:00.000",
		Rand:       fixedRand{},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := a.AuthorizeWithChallenge(
		`Unidirection algorithm="A:SM2;H:SM3;S:SM1/OFB/PKCS5;SI:SM3-SM2", random1="` + goldenRandom1 + `"`); err != nil {
		t.Fatalf("AuthorizeWithChallenge: %v", err)
	}
	pub := devicePublicKey(t, loadDeviceIdentity(t))
	cryptKey, err := EncryptVKEK(fixedRand{}, pub, goldenVKEK[:])
	if err != nil {
		t.Fatalf("EncryptVKEK: %v", err)
	}
	si := BuildSecurityInfo(SecurityInfo{Mode: ModeUnidirection, Algorithm: CapabilityAlgorithm, CryptKey: cryptKey})
	if err := a.VerifyOK(si); err != nil {
		t.Fatalf("VerifyOK: %v", err)
	}
	if got := a.VKEK(); !bytes.Equal(got, goldenVKEK[:]) {
		t.Fatalf("VKEK = % x, want % x", got, goldenVKEK)
	}
	return a
}

func TestVerifyIncomingNoteAcceptsValidSignature(t *testing.T) {
	a := establishedAuthenticator(t)

	const (
		method = "INVITE"
		from   = "<sip:34020000002000000001@3402000000>"
		to     = "<sip:34020000001320000001@3402000000>"
		callID = "plat-1"
		body   = "v=0\r\no=- 0 0 IN IP4 127.0.0.1\r\n"
	)
	date := FormatDate(time.Now())
	note := BuildNoteHeader(method, from, to, callID, date, goldenVKEK[:], body, VKEKRaw)

	if err := a.VerifyIncomingNote(method, from, to, callID, date, note, body); err != nil {
		t.Fatalf("valid Note rejected: %v", err)
	}
}

func TestVerifyIncomingNoteRejectsTamperedBody(t *testing.T) {
	a := establishedAuthenticator(t)

	const (
		method = "MESSAGE"
		from   = "<sip:34020000002000000001@3402000000>"
		to     = "<sip:34020000001320000001@3402000000>"
		callID = "plat-2"
	)
	date := FormatDate(time.Now())
	note := BuildNoteHeader(method, from, to, callID, date, goldenVKEK[:], "original-body", VKEKRaw)

	if err := a.VerifyIncomingNote(method, from, to, callID, date, note, "tampered-body"); err == nil {
		t.Fatal("tampered body must fail Note verification")
	}
}

func TestVerifyIncomingNotePassesWithoutNote(t *testing.T) {
	a := establishedAuthenticator(t)

	// Mixed-mode tolerance: a plain Digest platform sends no Note at all.
	if err := a.VerifyIncomingNote("MESSAGE", "f", "t", "c", FormatDate(time.Now()), "", ""); err != nil {
		t.Fatalf("Note-less request must pass: %v", err)
	}
}

func TestVerifyIncomingNoteRejectsStaleDate(t *testing.T) {
	a := establishedAuthenticator(t)

	const (
		method = "INVITE"
		from   = "f"
		to     = "t"
		callID = "plat-3"
		body   = "b"
	)
	// Self-consistent signature over a Date far outside the freshness
	// window: the digest verifies, only the replay window rejects it.
	stale := FormatDate(time.Now().Add(-2 * time.Hour))
	note := BuildNoteHeader(method, from, to, callID, stale, goldenVKEK[:], body, VKEKRaw)

	if err := a.VerifyIncomingNote(method, from, to, callID, stale, note, body); err == nil {
		t.Fatal("stale Date must fail the freshness window")
	}
}

func TestVerifyIncomingNoteRejectsMalformedDate(t *testing.T) {
	a := establishedAuthenticator(t)

	const (
		method = "INVITE"
		from   = "f"
		to     = "t"
		callID = "plat-4"
		body   = "b"
	)
	note := BuildNoteHeader(method, from, to, callID, "not-a-date", goldenVKEK[:], body, VKEKRaw)

	if err := a.VerifyIncomingNote(method, from, to, callID, "not-a-date", note, body); err == nil {
		t.Fatal("malformed Date must fail closed (it disables the freshness window)")
	}
}

func TestVerifyIncomingNoteRequiresSession(t *testing.T) {
	a, err := New(Options{
		Device:     loadDeviceIdentity(t),
		DeviceID:   goldenDevice,
		ServerID:   goldenServer,
		KeyVersion: "2026-01-01T00:00:00.000",
		Rand:       fixedRand{},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	date := FormatDate(time.Now())
	note := BuildNoteHeader("MESSAGE", "f", "t", "c", date, goldenVKEK[:], "b", VKEKRaw)

	if err := a.VerifyIncomingNote("MESSAGE", "f", "t", "c", date, note, "b"); err == nil {
		t.Fatal("a Note before the handshake completed must not verify")
	}
}
