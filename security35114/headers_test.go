//go:build gb35114

package security35114

import (
	"strings"
	"testing"
)

// Golden header strings pin the GB35114 A-level wire format as observed in
// published device↔platform captures. Any change to these strings is a
// breaking wire-format change and needs a major-version justification.

func TestBuildCapabilityAuthorization(t *testing.T) {
	got := BuildCapabilityAuthorization("2019-08-06T05:31:39", "")
	want := `Capability algorithm="A:SM2;H:SM3;S:SM4/OFB/PKCS5;SI:SM3-SM2", keyversion="2019-08-06T05:31:39"`
	if got != want {
		t.Fatalf("Capability header mismatch:\n got: %s\nwant: %s", got, want)
	}
}

func TestBuildCapabilityAuthorizationWithCert(t *testing.T) {
	certPEM := "-----BEGIN CERTIFICATE-----\nAAAA\n-----END CERTIFICATE-----\n"
	got := BuildCapabilityAuthorization("2019-08-06T05:31:39", certPEM)
	if !strings.Contains(got, `, cnonce="devicecert:`) {
		t.Fatalf("expected cnonce with devicecert prefix when cert included, got: %s", got)
	}
	if !strings.Contains(got, `LS0tLS1CRUdJTiBDRVJUSUZJQ0FURS0tLS0t`) {
		t.Fatalf("expected base64-encoded PEM in cnonce, got: %s", got)
	}
}

func TestParseChallengeUnidirection(t *testing.T) {
	ch, err := ParseChallenge(`Unidirection algorithm="A:SM2;H:SM3;S:SM1/OFB/PKCS5;SI:SM3-SM2", random1="PRAIIbutDbd5x/NKsbwwYw=="`)
	if err != nil {
		t.Fatalf("ParseChallenge: %v", err)
	}
	if ch.Mode != ModeUnidirection {
		t.Fatalf("mode = %q, want Unidirection", ch.Mode)
	}
	if ch.Random1 != goldenRandom1 {
		t.Fatalf("random1 = %q, want %q", ch.Random1, goldenRandom1)
	}
	if ch.Algorithm != "A:SM2;H:SM3;S:SM1/OFB/PKCS5;SI:SM3-SM2" {
		t.Fatalf("algorithm = %q", ch.Algorithm)
	}
}

func TestParseChallengeBidirection(t *testing.T) {
	ch, err := ParseChallenge(`Bidirection algorithm="A:SM2;H:SM3", random1="PRAIIbutDbd5x/NKsbwwYw=="`)
	if err != nil {
		t.Fatalf("ParseChallenge: %v", err)
	}
	if ch.Mode != ModeBidirection {
		t.Fatalf("mode = %q, want Bidirection", ch.Mode)
	}
}

func TestParseChallengeErrors(t *testing.T) {
	cases := []string{
		"",
		"Digest realm=\"3402000000\", nonce=\"abc\"",
		"Unidirection algorithm=\"A:SM2\"",
	}
	for _, c := range cases {
		if _, err := ParseChallenge(c); err == nil {
			t.Fatalf("ParseChallenge(%q) succeeded, want error (missing random1 or unknown scheme)", c)
		}
	}
}

func TestBuildAuthAuthorizationUnidirectionGolden(t *testing.T) {
	got := BuildAuthAuthorization(Challenge{Mode: ModeUnidirection, Random1: goldenRandom1},
		goldenRandom2, goldenServer, "", goldenSign1)
	want := `Unidirection random1="PRAIIbutDbd5x/NKsbwwYw==", random2="F4InuQewuMMqYPy1ItBdhQ==", serverid="34020000002000000001", sign1="MEUCIQD/9gP8olHM0TeLj0MxBRw3C8tQKFMMRgUupnyD4xXTTwIhAJvXxvTEDXj8Yk5qjHwujzUjpYpxxCGq7Zz0tKzhhJUU", algorithm="A:SM2;H:SM3;S:SM4/OFB/PKCS5;SI:SM3-SM2"`
	if got != want {
		t.Fatalf("Authorization mismatch:\n got: %s\nwant: %s", got, want)
	}
}

func TestBuildAuthAuthorizationBidirectionGolden(t *testing.T) {
	got := BuildAuthAuthorization(Challenge{Mode: ModeBidirection, Random1: goldenRandom1},
		goldenRandom2, goldenServer, goldenDevice, goldenSign1)
	want := `Bidirection random1="PRAIIbutDbd5x/NKsbwwYw==", random2="F4InuQewuMMqYPy1ItBdhQ==", serverid="34020000002000000001", deviceid="34020000001320000001", sign1="MEUCIQD/9gP8olHM0TeLj0MxBRw3C8tQKFMMRgUupnyD4xXTTwIhAJvXxvTEDXj8Yk5qjHwujzUjpYpxxCGq7Zz0tKzhhJUU", algorithm="A:SM2;H:SM3;S:SM4/OFB/PKCS5;SI:SM3-SM2"`
	if got != want {
		t.Fatalf("Authorization mismatch:\n got: %s\nwant: %s", got, want)
	}
}

func TestParseAuthAuthorizationGolden(t *testing.T) {
	aa, err := ParseAuthAuthorization(`Bidirection random1="` + goldenRandom1 + `", random2="` + goldenRandom2 + `", serverid="34020000002000000003", deviceid="` + goldenDevice + `", sign1="` + goldenSign1 + `", algorithm="A:SM2;H:SM3;S:SM4/OFB/PKCS5;SI:SM3-SM2"`)
	if err != nil {
		t.Fatalf("ParseAuthAuthorization: %v", err)
	}
	if aa.Mode != ModeBidirection || aa.Random1 != goldenRandom1 || aa.Random2 != goldenRandom2 ||
		aa.ServerID != "34020000002000000003" || aa.DeviceID != goldenDevice || aa.Sign1 != goldenSign1 {
		t.Fatalf("parsed %+v", aa)
	}
	if _, err := ParseAuthAuthorization(`Digest username="x"`); err == nil {
		t.Fatal("non-GB35114 Authorization accepted")
	}
	if _, err := ParseAuthAuthorization(`Unidirection random1="a", random2="b"`); err == nil {
		t.Fatal("Authorization missing serverid/sign1 accepted")
	}
}

func TestParseSecurityInfoUnidirectionGolden(t *testing.T) {
	si, err := ParseSecurityInfo(`Unidirection cryptkey="QUJDREVGR0hJSktMTU5PUFFSU1RVVldYWQ==", algorithm="A:SM2;H:SM3"`)
	if err != nil {
		t.Fatalf("ParseSecurityInfo: %v", err)
	}
	if si.Mode != ModeUnidirection {
		t.Fatalf("mode = %q", si.Mode)
	}
	if si.CryptKey != goldenCrypt {
		t.Fatalf("cryptkey = %q", si.CryptKey)
	}
}

func TestParseSecurityInfoBidirectionGolden(t *testing.T) {
	si, err := ParseSecurityInfo(`Bidirection algorithm="A:SM2;H:SM3;S:SM4/OFB/PKCS5,SM1/OFB/PKCS5;SI:SM3-SM2",random1="PRAIIbutDbd5x/NKsbwwYw==",random2="F4InuQewuMMqYPy1ItBdhQ==",deviceid="34020000001320000001",serverid="34020000002000000003",cryptkey="MHkCIBHIiuBM7BulVNA9W1lwMzqDWFgmwqmF3lUg2ek0OJ77AiEAhLUtNE+yGqjqOKSUDIMyaSuNTaI5NUkhLq/cDxHKXJwEIHFMxhef2Mm87QjLenmuVKs1rGm7Ls3aMG+1zPp47365BBAnTJVAmqz9pBE2xKOXhpQF",sign2="MEUCIQDzvGhJCuxmH/3NNtLNnrXIUOxYkYB7j8/3Th1LvjZHggIgD/nd9RbpEd6neZTuXDsIbNzydyS8WarbN1p6nHD5pHk="`)
	if err != nil {
		t.Fatalf("ParseSecurityInfo: %v", err)
	}
	if si.Mode != ModeBidirection {
		t.Fatalf("mode = %q", si.Mode)
	}
	if si.DeviceID != goldenDevice || si.ServerID != "34020000002000000003" {
		t.Fatalf("ids = %q/%q", si.DeviceID, si.ServerID)
	}
	if si.Random1 != goldenRandom1 || si.Random2 != goldenRandom2 {
		t.Fatalf("randoms = %q/%q", si.Random1, si.Random2)
	}
	if si.Sign2 != goldenSign2 {
		t.Fatalf("sign2 = %q", si.Sign2)
	}
}

func TestParseSecurityInfoErrors(t *testing.T) {
	if _, err := ParseSecurityInfo(`Unidirection algorithm="A:SM2;H:SM3"`); err == nil {
		t.Fatal("missing cryptkey accepted")
	}
	if _, err := ParseSecurityInfo(`Bidirection sign2="MEUCIQ=="`); err == nil {
		t.Fatal("bidirection without cryptkey accepted")
	}
}

func TestBuildSecurityInfoRoundTrip(t *testing.T) {
	in := SecurityInfo{
		Mode:      ModeBidirection,
		Algorithm: CapabilityAlgorithm,
		Random1:   goldenRandom1,
		Random2:   goldenRandom2,
		DeviceID:  goldenDevice,
		ServerID:  goldenServer,
		CryptKey:  goldenCrypt,
		Sign2:     goldenSign2,
	}
	built := BuildSecurityInfo(in)
	want := `Bidirection algorithm="A:SM2;H:SM3;S:SM4/OFB/PKCS5;SI:SM3-SM2",random1="PRAIIbutDbd5x/NKsbwwYw==",random2="F4InuQewuMMqYPy1ItBdhQ==",deviceid="34020000001320000001",serverid="34020000002000000001",cryptkey="QUJDREVGR0hJSktMTU5PUFFSU1RVVldYWQ==",sign2="MEUCIQDzvGhJCuxmH/3NNtLNnrXIUOxYkYB7j8/3Th1LvjZHggIgD/nd9RbpEd6neZTuXDsIbNzydyS8WarbN1p6nHD5pHk="`
	if built != want {
		t.Fatalf("SecurityInfo build mismatch:\n got: %s\nwant: %s", built, want)
	}
	back, err := ParseSecurityInfo(built)
	if err != nil {
		t.Fatalf("reparse: %v", err)
	}
	if back != in {
		t.Fatalf("round-trip mismatch:\n got: %+v\nwant: %+v", back, in)
	}
}

func TestBuildChallenge(t *testing.T) {
	if got := BuildChallenge(ModeBidirection, goldenRandom1); got != `Bidirection algorithm="A:SM2;H:SM3", random1="PRAIIbutDbd5x/NKsbwwYw=="` {
		t.Fatalf("Bidirection challenge = %q", got)
	}
	if got := BuildChallenge(ModeUnidirection, goldenRandom1); got != `Unidirection algorithm="A:SM2;H:SM3", random1="PRAIIbutDbd5x/NKsbwwYw=="` {
		t.Fatalf("Unidirection challenge = %q", got)
	}
}

func TestParseCapabilityAuthorization(t *testing.T) {
	// Without cnonce.
	got, err := ParseCapabilityAuthorization(`Capability algorithm="A:SM2;H:SM3;S:SM4/OFB/PKCS5;SI:SM3-SM2", keyversion="2026-01-01T00:00:00.000"`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got.Algorithm != CapabilityAlgorithm || got.KeyVersion != "2026-01-01T00:00:00.000" || got.DeviceCertPEM != "" {
		t.Fatalf("announcement = %+v", got)
	}

	// With the cnonce device certificate, the announcement round-trips the
	// exact PEM the device attached.
	certPEM := loadDeviceIdentity(t).CertPEM
	got, err = ParseCapabilityAuthorization(BuildCapabilityAuthorization("2026-01-01T00:00:00.000", certPEM))
	if err != nil {
		t.Fatalf("parse with cnonce: %v", err)
	}
	if got.KeyVersion != "2026-01-01T00:00:00.000" || got.DeviceCertPEM != certPEM {
		t.Fatalf("announcement with cnonce = %+v", got)
	}

	if _, err := ParseCapabilityAuthorization(`Digest realm="x"`); err == nil {
		t.Fatal("non-Capability Authorization accepted")
	}
	if _, err := ParseCapabilityAuthorization(`Capability keyversion="only"`); err == nil {
		t.Fatal("Capability without algorithm accepted")
	}
}
