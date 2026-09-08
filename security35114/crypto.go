//go:build gb35114

// SM2 signing and the VKEK envelope of GB35114 A-level, on top of
// emmansun/gmsm. Certificates follow GM/T 0015-2012 (SM2 X.509).

package security35114

import (
	"crypto/ecdsa"
	"crypto/rand"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/emmansun/gmsm/sm2"
	"github.com/emmansun/gmsm/smx509"
)

// Identity is one security entity's SM2 signing credential: the certificate
// presented to the peer and its private key. CertPEM keeps the source PEM
// so the certificate can be re-announced (Capability cnonce).
type Identity struct {
	Certificate *smx509.Certificate
	PrivateKey  *sm2.PrivateKey
	CertPEM     string
}

// LoadCertificate reads a PEM SM2 certificate (GM/T 0015-2012).
func LoadCertificate(certFile string) (*smx509.Certificate, error) {
	der, err := readPEM(certFile, "CERTIFICATE")
	if err != nil {
		return nil, err
	}
	cert, err := smx509.ParseCertificate(der)
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", certFile, err)
	}
	return cert, nil
}

// LoadIdentityFromFiles loads a certificate plus its private key. The key
// may be an SM2 SEC1 PEM ("SM2 PRIVATE KEY") or PKCS#8.
func LoadIdentityFromFiles(certFile, keyFile string) (*Identity, error) {
	cert, err := LoadCertificate(certFile)
	if err != nil {
		return nil, err
	}
	rawPEM, err := os.ReadFile(certFile)
	if err != nil {
		return nil, err
	}
	der, err := readPEM(keyFile, "PRIVATE KEY")
	if err != nil {
		return nil, err
	}
	if key, err := smx509.ParseSM2PrivateKey(der); err == nil {
		return &Identity{Certificate: cert, PrivateKey: key, CertPEM: string(rawPEM)}, nil
	}
	anyKey, err := smx509.ParsePKCS8PrivateKey(der)
	if err != nil {
		return nil, fmt.Errorf("parsing %s: neither SM2 SEC1 nor PKCS8: %w", keyFile, err)
	}
	key, ok := anyKey.(*sm2.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("%s: got %T, want an SM2 private key", keyFile, anyKey)
	}
	return &Identity{Certificate: cert, PrivateKey: key, CertPEM: string(rawPEM)}, nil
}

// readPEM decodes the first PEM block whose type contains want.
func readPEM(path, want string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, fmt.Errorf("%s: no PEM block found", path)
	}
	if !strings.Contains(block.Type, want) {
		return nil, fmt.Errorf("%s: PEM type %q does not contain %q", path, block.Type, want)
	}
	return block.Bytes, nil
}

// SignAuthPayload returns the octets signed by the device for sign1:
// random2 ‖ random1 ‖ SIP-server-ID, in the configured representation.
func SignAuthPayload(random1, random2, serverID string, enc RandomEncoding) []byte {
	switch enc {
	case ConcatRawBytes:
		out := append([]byte{}, decodeB64(random2)...)
		out = append(out, decodeB64(random1)...)
		return append(out, serverID...)
	default:
		return []byte(random2 + random1 + serverID)
	}
}

// Sign2Payload returns the octets signed by the platform for sign2:
// randoms ‖ device-ID ‖ cryptkey, operand order per order.
func Sign2Payload(random1, random2, deviceID, cryptKey string, order Sign2Order, enc RandomEncoding) []byte {
	if enc == ConcatRawBytes {
		first, second := decodeB64(random1), decodeB64(random2)
		if order == Sign2R2R1 {
			first, second = second, first
		}
		out := append([]byte{}, first...)
		out = append(out, second...)
		out = append(out, deviceID...)
		return append(out, cryptKey...)
	}
	if order == Sign2R2R1 {
		return []byte(random2 + random1 + deviceID + cryptKey)
	}
	return []byte(random1 + random2 + deviceID + cryptKey)
}

// SignMessage signs payload with SM2 (GM mode, default UID
// 1234567812345678) and returns the base64 DER signature.
func SignMessage(priv *sm2.PrivateKey, payload []byte) (string, error) {
	sig, err := sm2.SignASN1(rand.Reader, priv, payload, sm2.NewSM2SignerOption(true, nil))
	if err != nil {
		return "", fmt.Errorf("sm2 sign: %w", err)
	}
	return base64.StdEncoding.EncodeToString(sig), nil
}

// VerifyMessage verifies a base64 DER SM2 signature over payload with the
// certificate's public key (default UID).
func VerifyMessage(cert *smx509.Certificate, payload []byte, sigB64 string) error {
	pub, ok := cert.PublicKey.(*ecdsa.PublicKey)
	if !ok {
		return fmt.Errorf("certificate public key is %T, want an SM2 (EC) key", cert.PublicKey)
	}
	sig, err := base64.StdEncoding.DecodeString(sigB64)
	if err != nil {
		return fmt.Errorf("signature is not base64: %w", err)
	}
	if !sm2.VerifyASN1WithSM2(pub, nil, payload, sig) {
		return errors.New("SM2 signature verification failed")
	}
	return nil
}

// EncryptVKEK seals vkek to the device public key as the cryptkey value:
// base64 DER SM2 envelope (C1 ‖ C3 ‖ C2 per GB/T 32918.4). Used by the
// platform side; provided for conformance tests and UAS implementations.
func EncryptVKEK(random interface {
	Read(p []byte) (int, error)
}, pub *ecdsa.PublicKey, vkek []byte,
) (string, error) {
	der, err := sm2.EncryptASN1(random, pub, vkek)
	if err != nil {
		return "", fmt.Errorf("sm2 encrypt: %w", err)
	}
	return base64.StdEncoding.EncodeToString(der), nil
}

// DecryptVKEK opens the cryptkey envelope from a SecurityInfo header and
// returns the negotiated VKEK.
func DecryptVKEK(priv *sm2.PrivateKey, cryptKey string) ([]byte, error) {
	der, err := base64.StdEncoding.DecodeString(cryptKey)
	if err != nil {
		return nil, fmt.Errorf("cryptkey is not base64: %w", err)
	}
	vkek, err := sm2.Decrypt(priv, der)
	if err != nil {
		return nil, fmt.Errorf("sm2 decrypt: %w", err)
	}
	return vkek, nil
}

// decodeB64 decodes base64, falling back to the raw bytes on error (inputs
// come from peer headers; malformed values surface as signature/decrypt
// failures rather than panics here).
func decodeB64(s string) []byte {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return []byte(s)
	}
	return b
}

// ParseCertificatePEM parses a PEM SM2 certificate (GM/T 0015-2012) from
// bytes — e.g. a device certificate announced via the Capability cnonce.
func ParseCertificatePEM(pemBytes []byte) (*smx509.Certificate, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, errors.New("security35114: no PEM block found")
	}
	if !strings.Contains(block.Type, "CERTIFICATE") {
		return nil, fmt.Errorf("security35114: PEM type %q is not a certificate", block.Type)
	}
	cert, err := smx509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("security35114: parsing certificate: %w", err)
	}
	return cert, nil
}
