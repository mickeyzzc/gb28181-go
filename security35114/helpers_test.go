//go:build gb35114

package security35114

import (
	"crypto/ecdsa"
	"os"
	"path/filepath"
	"testing"

	"github.com/emmansun/gmsm/sm2"
	"github.com/emmansun/gmsm/smx509"
)

// Fixed golden inputs. random1/random2/serverid/sign1/cryptkey values are
// lifted from published GB35114 A-level captures so the wire format pinned
// here matches what real platforms emit; device/server IDs use the test
// fixture certificates' CNs.
const (
	goldenRandom1 = "PRAIIbutDbd5x/NKsbwwYw==" // 16 bytes, base64
	goldenRandom2 = "F4InuQewuMMqYPy1ItBdhQ==" // 16 bytes, base64
	goldenServer  = "34020000002000000001"     // SIP server / platform ID
	goldenDevice  = "34020000001320000001"     // FDWSF device ID
	goldenSign1   = "MEUCIQD/9gP8olHM0TeLj0MxBRw3C8tQKFMMRgUupnyD4xXTTwIhAJvXxvTEDXj8Yk5qjHwujzUjpYpxxCGq7Zz0tKzhhJUU"
	goldenSign2   = "MEUCIQDzvGhJCuxmH/3NNtLNnrXIUOxYkYB7j8/3Th1LvjZHggIgD/nd9RbpEd6neZTuXDsIbNzydyS8WarbN1p6nHD5pHk="
	goldenCrypt   = "QUJDREVGR0hJSktMTU5PUFFSU1RVVldYWQ==" // opaque placeholder envelope
	goldenDate    = "2024-01-31T14:40:49.583"
)

var goldenVKEK = [16]byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15}

func testdata(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("reading testdata/%s: %v", name, err)
	}
	return string(b)
}

// loadDeviceIdentity returns the committed SM2 signing identity of the test
// FDWSF (CN=34020000001320000001).
func loadDeviceIdentity(t *testing.T) *Identity {
	t.Helper()
	id, err := LoadIdentityFromFiles(
		filepath.Join("testdata", "device_cert.pem"),
		filepath.Join("testdata", "device_key.pem"),
	)
	if err != nil {
		t.Fatalf("loading device identity: %v", err)
	}
	return id
}

// loadPlatformIdentity returns the committed SM2 signing identity of the test
// SIP server (CN=34020000002000000001). Real platforms keep this key; tests
// use it to play the platform side of the handshake.
func loadPlatformIdentity(t *testing.T) *Identity {
	t.Helper()
	id, err := LoadIdentityFromFiles(
		filepath.Join("testdata", "platform_cert.pem"),
		filepath.Join("testdata", "platform_key.pem"),
	)
	if err != nil {
		t.Fatalf("loading platform identity: %v", err)
	}
	return id
}

func loadPlatformCert(t *testing.T) *smx509.Certificate {
	t.Helper()
	cert, err := LoadCertificate(filepath.Join("testdata", "platform_cert.pem"))
	if err != nil {
		t.Fatalf("loading platform cert: %v", err)
	}
	return cert
}

// fixedRand yields 0x01 bytes so challenge randoms — and, on the platform
// side of tests, the SM2 encryption ephemeral scalar — are deterministic
// and valid (all-zero k would make gmsm's encrypt loop retry forever).
type fixedRand struct{}

func (fixedRand) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 0x01
	}
	return len(p), nil
}

func devicePublicKey(t *testing.T, id *Identity) *ecdsa.PublicKey {
	t.Helper()
	pub, ok := id.Certificate.PublicKey.(*ecdsa.PublicKey)
	if !ok {
		t.Fatalf("device cert public key is %T, want *ecdsa.PublicKey", id.Certificate.PublicKey)
	}
	return pub
}

// vkekRoundTripChecker guards the SM2 envelope decode used by DecryptVKEK.
func mustDecryptVKEK(t *testing.T, priv *sm2.PrivateKey, cryptKey string) []byte {
	t.Helper()
	vkek, err := DecryptVKEK(priv, cryptKey)
	if err != nil {
		t.Fatalf("DecryptVKEK: %v", err)
	}
	return vkek
}
