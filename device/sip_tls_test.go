package device

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeTestCert generates a self-signed certificate + key into dir and
// returns (certPath, keyPath).
func writeTestCert(t *testing.T, dir string) (string, string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa: %v", err)
	}
	tmpl := x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "test-ca"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("x509: %v", err)
	}
	certPath := filepath.Join(dir, "ca.pem")
	keyPath := filepath.Join(dir, "ca-key.pem")
	certOut := &bytes.Buffer{}
	_ = pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err := os.WriteFile(certPath, certOut.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	keyOut := &bytes.Buffer{}
	_ = pem.Encode(keyOut, &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	if err := os.WriteFile(keyPath, keyOut.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return certPath, keyPath
}

func TestBuildTLSConfigDefaults(t *testing.T) {
	cfg, err := buildTLSConfig(Config{Transport: "tls"})
	if err != nil {
		t.Fatalf("buildTLSConfig: %v", err)
	}
	// Fail-closed default: no CA file and no explicit opt-out means system
	// roots verification (self-signed platforms must be pinned via TLSCAFile).
	if cfg.InsecureSkipVerify {
		t.Fatal("verification must stay enabled unless TLSInsecureSkipVerify is set")
	}
	if cfg.RootCAs != nil {
		t.Fatal("no CA file must not install a custom pool")
	}
}

func TestBuildTLSConfigWithCA(t *testing.T) {
	cert, key := writeTestCert(t, t.TempDir())
	cfg, err := buildTLSConfig(Config{Transport: "tls", TLSCAFile: cert, TLSCertFile: cert, TLSKeyFile: key})
	if err != nil {
		t.Fatalf("buildTLSConfig: %v", err)
	}
	if cfg.RootCAs == nil {
		t.Fatal("CA file must populate RootCAs")
	}
	if len(cfg.Certificates) != 1 {
		t.Fatalf("client pair must load, got %d certificates", len(cfg.Certificates))
	}
	if cfg.InsecureSkipVerify {
		t.Fatal("explicit CA must not skip verification")
	}
}

func TestBuildTLSConfigBadCA(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.pem")
	if err := os.WriteFile(bad, []byte("not a cert"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := buildTLSConfig(Config{Transport: "tls", TLSCAFile: bad}); err == nil {
		t.Fatal("garbage CA file must fail")
	}
}

func TestViaTransportLabel(t *testing.T) {
	s := &Server{}
	for _, tc := range []struct {
		transport, want string
	}{{"udp", "UDP"}, {"", "UDP"}, {"tcp", "TCP"}, {"tls", "TLS"}} {
		s.cfg.Transport = tc.transport
		if got := s.viaTransportLabel(); got != tc.want {
			t.Errorf("transport %q: got %q, want %q", tc.transport, got, tc.want)
		}
	}
}
