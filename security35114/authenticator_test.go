//go:build gb35114

package security35114

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"strings"
	"testing"
)

func newTestAuthenticator(t *testing.T) *Authenticator {
	t.Helper()
	a, err := New(Options{
		Device:     loadDeviceIdentity(t),
		DeviceID:   goldenDevice,
		ServerID:   goldenServer,
		KeyVersion: "2026-01-01T00:00:00.000",
		Rand:       rand.Reader,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return a
}

func TestNewValidation(t *testing.T) {
	if _, err := New(Options{DeviceID: "1", ServerID: "2"}); err == nil {
		t.Fatal("New without device identity accepted")
	}
	if _, err := New(Options{Device: loadDeviceIdentity(t)}); err == nil {
		t.Fatal("New without IDs accepted")
	}
}

func TestInitialAuthorization(t *testing.T) {
	a := newTestAuthenticator(t)
	got := a.InitialAuthorization()
	want := `Capability algorithm="A:SM2;H:SM3;S:SM4/OFB/PKCS5;SI:SM3-SM2", keyversion="2026-01-01T00:00:00.000"`
	if got != want {
		t.Fatalf("initial authorization:\n got: %s\nwant: %s", got, want)
	}
	if a.InitialAuthorization() != want {
		t.Fatal("initial authorization not stable across calls")
	}
}

func TestInitialAuthorizationDefaultKeyVersion(t *testing.T) {
	a, err := New(Options{
		Device:   loadDeviceIdentity(t),
		DeviceID: goldenDevice,
		ServerID: goldenServer,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if !strings.Contains(a.InitialAuthorization(), `keyversion="`) {
		t.Fatalf("no default keyversion: %s", a.InitialAuthorization())
	}
}

func TestHandshakeUnidirection(t *testing.T) {
	dev := loadDeviceIdentity(t)
	a, err := New(Options{
		Device:     dev,
		DeviceID:   goldenDevice,
		ServerID:   goldenServer,
		KeyVersion: "2026-01-01T00:00:00.000",
		Rand:       fixedRand{},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	authHeader, err := a.AuthorizeWithChallenge(
		`Unidirection algorithm="A:SM2;H:SM3;S:SM1/OFB/PKCS5;SI:SM3-SM2", random1="` + goldenRandom1 + `"`)
	if err != nil {
		t.Fatalf("AuthorizeWithChallenge: %v", err)
	}
	if a.Mode() != ModeUnidirection {
		t.Fatalf("mode = %q", a.Mode())
	}

	// Parse the produced header back and validate every field: random1 is
	// echoed, random2 is the fixed reader's output, sign1 verifies against
	// the device certificate.
	chBack, rest := splitScheme(authHeader)
	if chBack != "Unidirection" {
		t.Fatalf("scheme = %q", chBack)
	}
	params := parseParams(rest)
	if params["random1"] != goldenRandom1 {
		t.Fatalf("random1 echo = %q", params["random1"])
	}
	wantRandom2 := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 16))
	if params["random2"] != wantRandom2 {
		t.Fatalf("random2 = %q, want %q", params["random2"], wantRandom2)
	}
	if params["serverid"] != goldenServer {
		t.Fatalf("serverid = %q", params["serverid"])
	}
	if _, ok := params["deviceid"]; ok {
		t.Fatalf("unidirection Authorization must not carry deviceid: %s", authHeader)
	}
	payload := SignAuthPayload(params["random1"], params["random2"], goldenServer, ConcatWireStrings)
	if err := VerifyMessage(dev.Certificate, payload, params["sign1"]); err != nil {
		t.Fatalf("sign1 does not verify: %v", err)
	}

	// Platform side: seal the VKEK to the device key and answer.
	pub := devicePublicKey(t, dev)
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
}

func TestHandshakeBidirection(t *testing.T) {
	dev := loadDeviceIdentity(t)
	plat := loadPlatformIdentity(t)
	a, err := New(Options{
		Device:       dev,
		PlatformCert: plat.Certificate,
		DeviceID:     goldenDevice,
		ServerID:     goldenServer,
		KeyVersion:   "2026-01-01T00:00:00.000",
		Sign2Order:   Sign2R1R2,
		Rand:         fixedRand{},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	authHeader, err := a.AuthorizeWithChallenge(
		`Bidirection algorithm="A:SM2;H:SM3", random1="` + goldenRandom1 + `"`)
	if err != nil {
		t.Fatalf("AuthorizeWithChallenge: %v", err)
	}
	if a.Mode() != ModeBidirection {
		t.Fatalf("mode = %q", a.Mode())
	}
	params := headerParams(t, authHeader)
	if params["deviceid"] != goldenDevice {
		t.Fatalf("deviceid = %q", params["deviceid"])
	}

	// Platform side: seal VKEK, sign R1R2+deviceid+cryptkey, answer.
	pub := devicePublicKey(t, dev)
	cryptKey, err := EncryptVKEK(fixedRand{}, pub, goldenVKEK[:])
	if err != nil {
		t.Fatalf("EncryptVKEK: %v", err)
	}
	sign2, err := SignMessage(plat.PrivateKey, Sign2Payload(goldenRandom1, params["random2"], goldenDevice, cryptKey, Sign2R1R2, ConcatWireStrings))
	if err != nil {
		t.Fatalf("SignMessage(sign2): %v", err)
	}
	si := BuildSecurityInfo(SecurityInfo{
		Mode: ModeBidirection, Algorithm: CapabilityAlgorithm,
		Random1: goldenRandom1, Random2: params["random2"],
		DeviceID: goldenDevice, ServerID: goldenServer,
		CryptKey: cryptKey, Sign2: sign2,
	})
	if err := a.VerifyOK(si); err != nil {
		t.Fatalf("VerifyOK: %v", err)
	}
	if !bytes.Equal(a.VKEK(), goldenVKEK[:]) {
		t.Fatalf("VKEK = % x", a.VKEK())
	}
}

func TestVerifyOKRejections(t *testing.T) {
	dev := loadDeviceIdentity(t)
	plat := loadPlatformIdentity(t)
	pub := devicePublicKey(t, dev)
	cryptKey, err := EncryptVKEK(fixedRand{}, pub, goldenVKEK[:])
	if err != nil {
		t.Fatalf("EncryptVKEK: %v", err)
	}

	// Bad base64 cryptkey.
	a := newTestAuthenticator(t)
	if err := a.VerifyOK(`Unidirection cryptkey="###", algorithm="A:SM2;H:SM3"`); err == nil {
		t.Fatal("invalid cryptkey accepted")
	}

	// Bidirection without a platform certificate configured.
	noPlat, err := New(Options{Device: dev, DeviceID: goldenDevice, ServerID: goldenServer, Rand: fixedRand{}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := noPlat.AuthorizeWithChallenge(`Bidirection algorithm="A:SM2;H:SM3", random1="` + goldenRandom1 + `"`); err != nil {
		t.Fatalf("AuthorizeWithChallenge: %v", err)
	}
	if err := noPlat.VerifyOK(`Bidirection random1="` + goldenRandom1 + `",random2="` + goldenRandom2 + `",deviceid="` + goldenDevice + `",serverid="` + goldenServer + `",cryptkey="` + cryptKey + `",sign2="AA=="`); err == nil {
		t.Fatal("bidirection VerifyOK without platform certificate accepted")
	}

	// Tampered sign2.
	a2, err := New(Options{
		Device: dev, PlatformCert: plat.Certificate,
		DeviceID: goldenDevice, ServerID: goldenServer, Sign2Order: Sign2R1R2, Rand: fixedRand{},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := a2.AuthorizeWithChallenge(`Bidirection algorithm="A:SM2;H:SM3", random1="` + goldenRandom1 + `"`); err != nil {
		t.Fatalf("AuthorizeWithChallenge: %v", err)
	}
	goodSign2, err := SignMessage(plat.PrivateKey, Sign2Payload(goldenRandom1, goldenRandom2, goldenDevice, cryptKey, Sign2R1R2, ConcatWireStrings))
	if err != nil {
		t.Fatalf("SignMessage: %v", err)
	}
	bad := BuildSecurityInfo(SecurityInfo{
		Mode: ModeBidirection, Algorithm: CapabilityAlgorithm,
		Random1: goldenRandom1, Random2: goldenRandom2,
		DeviceID: goldenDevice, ServerID: goldenServer,
		CryptKey: cryptKey, Sign2: tamperBase64(t, goodSign2),
	})
	if err := a2.VerifyOK(bad); err == nil {
		t.Fatal("tampered sign2 accepted")
	}

	// Random mismatch between challenge and SecurityInfo.
	a3, err := New(Options{
		Device: dev, PlatformCert: plat.Certificate,
		DeviceID: goldenDevice, ServerID: goldenServer, Rand: fixedRand{},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := a3.AuthorizeWithChallenge(`Bidirection algorithm="A:SM2;H:SM3", random1="` + goldenRandom1 + `"`); err != nil {
		t.Fatalf("AuthorizeWithChallenge: %v", err)
	}
	swappedSign2, err := SignMessage(plat.PrivateKey, Sign2Payload(goldenRandom2, goldenRandom1, goldenDevice, cryptKey, Sign2R1R2, ConcatWireStrings))
	if err != nil {
		t.Fatalf("SignMessage: %v", err)
	}
	swapped := BuildSecurityInfo(SecurityInfo{
		Mode: ModeBidirection, Algorithm: CapabilityAlgorithm,
		Random1: goldenRandom2, Random2: goldenRandom1, // echoed randoms swapped
		DeviceID: goldenDevice, ServerID: goldenServer,
		CryptKey: cryptKey, Sign2: swappedSign2,
	})
	if err := a3.VerifyOK(swapped); err == nil {
		t.Fatal("SecurityInfo with non-matching randoms accepted")
	}
}

func TestRandom2Uniqueness(t *testing.T) {
	a := newTestAuthenticator(t)
	h1, err := a.AuthorizeWithChallenge(`Unidirection algorithm="A:SM2;H:SM3", random1="` + goldenRandom1 + `"`)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	h2, err := a.AuthorizeWithChallenge(`Unidirection algorithm="A:SM2;H:SM3", random1="` + goldenRandom1 + `"`)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	r1 := headerParams(t, h1)["random2"]
	r2 := headerParams(t, h2)["random2"]
	if r1 == r2 {
		t.Fatalf("random2 repeated across handshakes: %q", r1)
	}
}

func TestChallengeBeforeCapability(t *testing.T) {
	a := newTestAuthenticator(t)
	if err := a.VerifyOK(`Unidirection cryptkey="` + goldenCrypt + `", algorithm="A:SM2;H:SM3"`); err == nil {
		t.Fatal("VerifyOK before AuthorizeWithChallenge accepted")
	}
	if a.Mode() != "" {
		t.Fatalf("mode before challenge = %q", a.Mode())
	}
}

func tamperBase64(t *testing.T, b64 string) string {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		t.Fatalf("tamper fixture: %v", err)
	}
	raw[len(raw)-1] ^= 0xFF
	return base64.StdEncoding.EncodeToString(raw)
}

func headerParams(t *testing.T, header string) map[string]string {
	t.Helper()
	scheme, rest := splitScheme(header)
	if scheme == "" {
		t.Fatalf("empty scheme in %q", header)
	}
	return parseParams(rest)
}
