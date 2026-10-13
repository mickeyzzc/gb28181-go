package device

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
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
	}{{"udp", "UDP"}, {"", "UDP"}, {"tcp", "TCP"}, {"tcp-out", "TCP"}, {"tls", "TLS"}} {
		s.cfg.Transport = tc.transport
		if got := s.viaTransportLabel(); got != tc.want {
			t.Errorf("transport %q: got %q, want %q", tc.transport, got, tc.want)
		}
	}
}

// The outbound stream transports dial the platform and heal a dropped
// connection on the next re-registration cycle (ensureStreamConn).
func TestDialStream_TCPOut_ConnectsAndRedials(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port

	s := &Server{}
	s.cfg = Config{
		Enabled:            true,
		Transport:          "tcp-out",
		PlatformSIPAddress: "127.0.0.1",
		PlatformSIPPort:    port,
		DeviceID:           "34020000001320000001",
		SIPDomain:          "3402000000",
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	accept := func() net.Conn {
		conn, err := listener.Accept()
		if err != nil {
			t.Fatalf("accept: %v", err)
		}
		return conn
	}

	// 1) Initial dial connects and registers the stream.
	if err := s.dialStream(ctx); err != nil {
		t.Fatalf("dialStream: %v", err)
	}
	platformSide := accept()
	s.mu.Lock()
	remote := s.streamRemote
	s.mu.Unlock()
	if remote == "" {
		t.Fatal("streamRemote not recorded")
	}
	if _, ok := s.tcpConns.Load(remote); !ok {
		t.Fatal("stream connection not registered in tcpConns")
	}

	// 2) The platform drops the connection: the read loop removes the
	// entry on exit.
	_ = platformSide.Close()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, ok := s.tcpConns.Load(remote); !ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("dropped stream connection still registered after 2s")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// 3) The re-registration cycle re-dials and the fresh connection
	// takes over the slot.
	s.ensureStreamConn(ctx)
	platformSide2 := accept()
	defer platformSide2.Close()
	s.mu.Lock()
	remote2 := s.streamRemote
	s.mu.Unlock()
	if _, ok := s.tcpConns.Load(remote2); !ok {
		t.Fatal("re-dialed stream connection not registered")
	}
}
