//go:build gb35114

package security35114

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"strings"
	"testing"
)

// Payload goldens pin the exact octets fed to SM2 sign/verify. The hex was
// produced once and hardcoded; changing it means changing the wire contract.

func TestSignAuthPayloadWireStringsGolden(t *testing.T) {
	got := SignAuthPayload(goldenRandom1, goldenRandom2, goldenServer, ConcatWireStrings)
	want := "4634496e75516577754d4d71595079314974426468513d3d505241494962757444626435782f4e4b7362777759773d3d3334303230303030303032303030303030303031"
	if hex.EncodeToString(got) != want {
		t.Fatalf("sign1 payload (wire strings):\n got: %s\nwant: %s", hex.EncodeToString(got), want)
	}
}

func TestSignAuthPayloadRawBytesGolden(t *testing.T) {
	got := SignAuthPayload(goldenRandom1, goldenRandom2, goldenServer, ConcatRawBytes)
	want := "178227b907b0b8c32a60fcb522d05d853d100821bbad0db779c7f34ab1bc30633334303230303030303032303030303030303031"
	if hex.EncodeToString(got) != want {
		t.Fatalf("sign1 payload (raw bytes):\n got: %s\nwant: %s", hex.EncodeToString(got), want)
	}
}

func TestSign2PayloadGoldens(t *testing.T) {
	r1r2 := Sign2Payload(goldenRandom1, goldenRandom2, goldenDevice, goldenCrypt, Sign2R1R2, ConcatWireStrings)
	wantR1R2 := "505241494962757444626435782f4e4b7362777759773d3d4634496e75516577754d4d71595079314974426468513d3d333430323030303030303133323030303030303151554a44524556475230684a536b744d545535505546465355315256566c645957513d3d"
	if hex.EncodeToString(r1r2) != wantR1R2 {
		t.Fatalf("sign2 payload (R1R2):\n got: %s\nwant: %s", hex.EncodeToString(r1r2), wantR1R2)
	}
	r2r1 := Sign2Payload(goldenRandom1, goldenRandom2, goldenDevice, goldenCrypt, Sign2R2R1, ConcatWireStrings)
	wantR2R1 := "4634496e75516577754d4d71595079314974426468513d3d505241494962757444626435782f4e4b7362777759773d3d333430323030303030303133323030303030303151554a44524556475230684a536b744d545535505546465355315256566c645957513d3d"
	if hex.EncodeToString(r2r1) != wantR2R1 {
		t.Fatalf("sign2 payload (R2R1):\n got: %s\nwant: %s", hex.EncodeToString(r2r1), wantR2R1)
	}
}

func TestSignVerifyRoundTrip(t *testing.T) {
	dev := loadDeviceIdentity(t)
	plat := loadPlatformIdentity(t)
	payload := SignAuthPayload(goldenRandom1, goldenRandom2, goldenServer, ConcatWireStrings)

	sig, err := SignMessage(dev.PrivateKey, payload)
	if err != nil {
		t.Fatalf("SignMessage: %v", err)
	}
	if raw, err := base64.StdEncoding.DecodeString(sig); err != nil || len(raw) < 8 || raw[0] != 0x30 {
		t.Fatalf("sign1 is not base64 DER: %q (err=%v)", sig, err)
	}
	if err := VerifyMessage(dev.Certificate, payload, sig); err != nil {
		t.Fatalf("VerifyMessage with device cert: %v", err)
	}
	if err := VerifyMessage(plat.Certificate, payload, sig); err == nil {
		t.Fatal("signature verified with wrong certificate")
	}
	tampered := append([]byte{}, payload...)
	tampered[0] ^= 0xFF
	if err := VerifyMessage(dev.Certificate, tampered, sig); err == nil {
		t.Fatal("signature verified over tampered payload")
	}
}

func TestVKEKEnvelopeRoundTrip(t *testing.T) {
	dev := loadDeviceIdentity(t)
	pub := devicePublicKey(t, dev)

	cryptKey, err := EncryptVKEK(fixedRand{}, pub, goldenVKEK[:])
	if err != nil {
		t.Fatalf("EncryptVKEK: %v", err)
	}
	der, err := base64.StdEncoding.DecodeString(cryptKey)
	if err != nil {
		t.Fatalf("cryptkey not base64: %v", err)
	}
	if der[0] != 0x30 {
		t.Fatalf("cryptkey DER does not start with SEQUENCE: % x", der[0])
	}
	// The envelope must decode to x,y INTEGERs + 32-byte hash OCTET STRING +
	// 16-byte ciphertext OCTET STRING (VKEK length) — 121 content bytes total
	// for a 16-byte plaintext.
	if der[1] != 121 {
		t.Fatalf("cryptkey SEQUENCE length = %d, want 121 (16-byte VKEK)", der[1])
	}
	got := mustDecryptVKEK(t, dev.PrivateKey, cryptKey)
	if !bytes.Equal(got, goldenVKEK[:]) {
		t.Fatalf("VKEK round-trip mismatch: % x", got)
	}

	// Decrypting with the platform key must fail (envelope is bound to the
	// device public key).
	plat := loadPlatformIdentity(t)
	if _, err := DecryptVKEK(plat.PrivateKey, cryptKey); err == nil {
		t.Fatal("cryptkey decrypted with wrong private key")
	}
	if _, err := DecryptVKEK(dev.PrivateKey, "!!!not-base64!!!"); err == nil {
		t.Fatal("invalid base64 cryptkey accepted")
	}
}

func TestLoadIdentityAndCertificate(t *testing.T) {
	dev := loadDeviceIdentity(t)
	if cn := dev.Certificate.Subject.CommonName; cn != goldenDevice {
		t.Fatalf("device cert CN = %q, want %q", cn, goldenDevice)
	}
	plat := loadPlatformCert(t)
	if cn := plat.Subject.CommonName; cn != goldenServer {
		t.Fatalf("platform cert CN = %q, want %q", cn, goldenServer)
	}
	if strings.TrimSpace(testdata(t, "device_key.pem")) == "" {
		t.Fatal("empty device key fixture")
	}
}
