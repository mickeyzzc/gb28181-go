//go:build gb35114

package security35114

import (
	"bytes"
	"strings"
	"testing"

	"github.com/emmansun/gmsm/smx509"
)

func TestVEKEnvelopeRoundTrip(t *testing.T) {
	vkek := make([]byte, 16)
	for i := range vkek {
		vkek[i] = byte(i)
	}
	vek, err := GenerateVEK(nil)
	if err != nil {
		t.Fatalf("GenerateVEK: %v", err)
	}
	env, err := SealVEK(vkek, vek)
	if err != nil {
		t.Fatalf("SealVEK: %v", err)
	}
	if len(env) != 24 { // 16 bytes → base64 = 24 chars
		t.Fatalf("envelope length = %d, want 24", len(env))
	}
	opened, err := OpenVEK(vkek, env)
	if err != nil {
		t.Fatalf("OpenVEK: %v", err)
	}
	if !bytes.Equal(opened, vek) {
		t.Fatalf("opened VEK %x != original %x", opened, vek)
	}

	// A wrong VKEK yields a wrong VEK without error (ECB is malleable here);
	// wrong lengths must fail outright.
	bad := append([]byte(nil), vkek...)
	bad[0] ^= 0xFF
	if other, err := OpenVEK(bad, env); err != nil || bytes.Equal(other, vek) {
		t.Fatalf("wrong VKEK must open a different VEK (other==orig: %v, err: %v)", bytes.Equal(other, vek), err)
	}
	if _, err := OpenVEK(vkek[:15], env); err == nil {
		t.Fatal("short VKEK must fail")
	}
	if _, err := OpenVEK(vkek, "!!!not-base64!!!"); err == nil {
		t.Fatal("non-base64 envelope must fail")
	}
}

func TestPacketCryptorLossResilient(t *testing.T) {
	vek, _ := GenerateVEK(nil)
	crypt, err := NewPacketCryptor(vek)
	if err != nil {
		t.Fatalf("NewPacketCryptor: %v", err)
	}
	const ssrc = uint32(1234567890)
	p1 := bytes.Repeat([]byte{0xA5}, 1400)
	p2 := bytes.Repeat([]byte{0x5A}, 900)

	e1 := crypt(p1, ssrc, 100)
	e2 := crypt(p2, ssrc, 101)
	if bytes.Equal(e1, p1) || bytes.Equal(e2, p2) {
		t.Fatal("encryption must change the payload")
	}
	// Same (ssrc, seq) keystream: XOR twice restores.
	if got := crypt(e1, ssrc, 100); !bytes.Equal(got, p1) {
		t.Fatal("round-trip with same seq failed")
	}
	// Packet loss resilience: decrypt packet 2 without ever seeing packet 1.
	if got := crypt(e2, ssrc, 101); !bytes.Equal(got, p2) {
		t.Fatal("decrypt after dropped predecessor failed")
	}
	// Distinct seqs must not share a keystream prefix.
	if bytes.Equal(e1[:16], e2[:16]) {
		t.Fatal("adjacent sequence numbers produced identical keystreams")
	}
	// Distinct IVs per (ssrc, seq).
	if bytes.Equal(PacketIV(vek, ssrc, 1), PacketIV(vek, ssrc, 2)) {
		t.Fatal("PacketIV must differ across seq")
	}
	if bytes.Equal(PacketIV(vek, ssrc, 1), PacketIV(vek, ssrc+1, 1)) {
		t.Fatal("PacketIV must differ across ssrc")
	}
}

func TestAUSignRoundTripAndTamper(t *testing.T) {
	devIdentity, err := LoadIdentityFromFiles(
		"testdata/device_cert.pem", "testdata/device_key.pem")
	if err != nil {
		t.Fatalf("device identity: %v", err)
	}
	au := []byte("\x00\x00\x01\xba pseudo PS access unit payload")
	const ssrc, ts = uint32(987654321), uint32(90000)

	sig, err := SignAU(devIdentity.PrivateKey, au, ssrc, ts)
	if err != nil {
		t.Fatalf("SignAU: %v", err)
	}
	if len(sig) < 64 {
		t.Fatalf("DER signature suspiciously short: %d", len(sig))
	}
	if err := VerifyAU(devIdentity.Certificate, au, ssrc, ts, sig); err != nil {
		t.Fatalf("VerifyAU: %v", err)
	}
	// Tampered payload, wrong binding, wrong signature all fail.
	tampered := append([]byte(nil), au...)
	tampered[len(tampered)-1] ^= 1
	if err := VerifyAU(devIdentity.Certificate, tampered, ssrc, ts, sig); err == nil {
		t.Fatal("tampered AU verified")
	}
	if err := VerifyAU(devIdentity.Certificate, au, ssrc, ts+1, sig); err == nil {
		t.Fatal("wrong timestamp binding verified")
	}
	badSig := append([]byte(nil), sig...)
	badSig[10] ^= 0xFF
	if err := VerifyAU(devIdentity.Certificate, au, ssrc, ts, badSig); err == nil {
		t.Fatal("corrupted signature verified")
	}
}

func TestSDPSecurityRoundTrip(t *testing.T) {
	// Offer forms.
	if got := SDPSecurityOffer(LevelB); got != "GB35114-B" {
		t.Fatalf("B offer = %q", got)
	}
	if got := SDPSecurityOffer(LevelC); got != "GB35114-C" {
		t.Fatalf("C offer = %q", got)
	}
	// Answer forms.
	vkek := make([]byte, 16)
	vek, _ := GenerateVEK(nil)
	env, err := SealVEK(vkek, vek)
	if err != nil {
		t.Fatalf("SealVEK: %v", err)
	}
	if got := SDPSecurityAnswer(LevelB, ""); got != "GB35114-B" {
		t.Fatalf("B answer = %q", got)
	}
	got := SDPSecurityAnswer(LevelC, env)
	if !strings.HasPrefix(got, "GB35114-C,vek=") || !strings.HasSuffix(got, env) {
		t.Fatalf("C answer = %q", got)
	}

	sec, err := ParseSDPSecurity(got)
	if err != nil {
		t.Fatalf("ParseSDPSecurity: %v", err)
	}
	if sec.Level != LevelC || sec.VEKEnvelope != env {
		t.Fatalf("parsed %+v", sec)
	}
	sec, err = ParseSDPSecurity("GB35114-B")
	if err != nil || sec.Level != LevelB || sec.VEKEnvelope != "" {
		t.Fatalf("plain B parse = %+v, %v", sec, err)
	}
	for _, bad := range []string{"", "GB35114-D", "SSAV", "GB35114-C,gek=abc"} {
		if _, err := ParseSDPSecurity(bad); err == nil {
			t.Fatalf("bad value %q parsed", bad)
		}
	}
}

func TestPlatformArmMediaSecurity(t *testing.T) {
	dev := loadDeviceIdentity(t)
	platIdentity := loadPlatformIdentity(t)
	a := newLoopbackDevice(t, dev, platIdentity.Certificate, goldenServer)
	p := newTestPlatformLevel(t, dev.Certificate, LevelC)
	if got := p.Level(); got != LevelC {
		t.Fatalf("level = %q", got)
	}
	if got := p.MediaSDPOffer(); got != "GB35114-C" {
		t.Fatalf("offer = %q", got)
	}

	// Complete the REGISTER handshake so the device holds a VKEK.
	wwwAuth, err := p.Challenge(goldenDevice, a.InitialAuthorization())
	if err != nil {
		t.Fatalf("Challenge: %v", err)
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
		t.Fatalf("VerifyOK: %v", err)
	}

	// The device answers a C offer with a sealed VEK; arming yields both
	// hooks and the decryptor round-trips a payload.
	answerValue, encrypt, err := a.AnswerMediaSecurity("GB35114-C")
	if err != nil {
		t.Fatalf("AnswerMediaSecurity: %v", err)
	}
	if encrypt == nil {
		t.Fatal("C answer must carry an encryptor")
	}
	answerSDP := "v=0\r\ns=Play\r\ny=123\r\na=security:" + answerValue + "\r\n"
	decrypt, verifyAU, err := p.ArmMediaSecurity(goldenDevice, []byte(answerSDP))
	if err != nil {
		t.Fatalf("ArmMediaSecurity: %v", err)
	}
	if decrypt == nil || verifyAU == nil {
		t.Fatal("C arming must yield decrypt + AU verifier")
	}
	plain := []byte("gb35114 media payload")
	if got := decrypt(encrypt(plain, 7, 42), 7, 42); !bytes.Equal(got, plain) {
		t.Fatal("decrypt(encrypt(payload)) != payload")
	}

	// Signature verification through the armed closure: valid passes,
	// tampered fails.
	sig, err := a.SignAU(plain, 7, 42)
	if err != nil {
		t.Fatalf("SignAU: %v", err)
	}
	if err := verifyAU(plain, 7, 42, sig); err != nil {
		t.Fatalf("verifyAU(valid): %v", err)
	}
	if err := verifyAU(append(plain, '!'), 7, 42, sig); err == nil {
		t.Fatal("verifyAU(tampered) passed")
	}

	// No a=security in the answer → nil hooks, no error.
	decrypt, verifyAU, err = p.ArmMediaSecurity(goldenDevice, []byte("v=0\r\ns=Play\r\ny=123\r\n"))
	if err != nil || decrypt != nil || verifyAU != nil {
		t.Fatalf("plaintext answer must arm nothing: decrypt=%t verify=%t err=%v", decrypt != nil, verifyAU != nil, err)
	}

	// A B answer arms only the verifier.
	decrypt, verifyAU, err = p.ArmMediaSecurity(goldenDevice, []byte("v=0\r\na=security:GB35114-B\r\n"))
	if err != nil || decrypt != nil || verifyAU == nil {
		t.Fatalf("B answer must arm verifier only: decrypt=%t verify=%t err=%v", decrypt != nil, verifyAU != nil, err)
	}

	// A C answer without a prior handshake fails closed.
	if _, _, err := p.ArmMediaSecurity("34020000001320000099", []byte("a=security:GB35114-C,vek=AAAA\r\n")); err == nil {
		t.Fatal("unknown device must fail closed")
	}
}

// newTestPlatformLevel is newTestPlatform with an explicit security level.
func newTestPlatformLevel(t *testing.T, devCert *smx509.Certificate, level Level) *Platform {
	t.Helper()
	p, err := NewPlatform(PlatformConfig{
		ServerID:    goldenServer,
		Identity:    loadPlatformIdentity(t),
		DeviceCerts: map[string]*smx509.Certificate{goldenDevice: devCert},
		Rand:        fixedRand{},
		Level:       level,
	})
	if err != nil {
		t.Fatalf("NewPlatform: %v", err)
	}
	return p
}
