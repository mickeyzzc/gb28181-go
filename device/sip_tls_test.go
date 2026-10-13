package device

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
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

// The inbound SIPS listener handshakes with the device's certificate
// and dispatches framed SIP through the shared reader.
func TestTLSListenerHandshakeAndDispatch(t *testing.T) {
	// Mint the device pair with the existing self-signed helper.
	dir := t.TempDir()
	caPath, keyPathOrig := writeTestCert(t, dir)
	certPath := caPath // the self-signed cert serves as the device identity
	keyPath := keyPathOrig

	s := &Server{}
	s.cfg = Config{
		Enabled:      true,
		Transport:    "tls-listen",
		TLSCertFile:  certPath,
		TLSKeyFile:   keyPath,
		LocalSIPPort: 0,
		SIPDomain:    "3402000000",
		DeviceID:     "34020000001320000001",
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		if err := s.startTLSListener(ctx); err != nil {
			t.Logf("listener: %v", err)
		}
	}()

	// Wait for the listener to bind: retry dial until success.
	var conn net.Conn
	deadline := time.Now().Add(2 * time.Second)
	for {
		port := 0
		s.mu.Lock()
		if s.tcpListener != nil {
			port = s.tcpListener.Addr().(*net.TCPAddr).Port
		}
		s.mu.Unlock()
		if port == 0 {
			if time.Now().After(deadline) {
				t.Fatal("listener never bound")
			}
			time.Sleep(10 * time.Millisecond)
			continue
		}
		c, err := tls.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port), &tls.Config{
			InsecureSkipVerify: true, //nolint:gosec // self-signed test cert
		})
		if err == nil {
			conn = c
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("TLS dial: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	defer conn.Close()

	// Send a framed MESSAGE (DeviceInfo query) through the TLS stream.
	body := "<Query><CmdType>DeviceInfo</CmdType><SN>1</SN><DeviceID>34020000001320000001</DeviceID></Query>"
	frame := fmt.Sprintf("MESSAGE sip:34020000001320000001@3402000000 SIP/2.0\r\nVia: SIP/2.0/TLS 127.0.0.1:9;branch=z9hG4bKtl1\r\nFrom: <sip:34020000002000000001@3402000000>;tag=p\r\nTo: <sip:34020000001320000001@3402000000>\r\nCall-ID: tls-li-1\r\nCSeq: 1 MESSAGE\r\nContent-Type: Application/MANSCDP+xml\r\nContent-Length: %d\r\n\r\n%s", len(body), body)
	if _, err := conn.Write([]byte(frame)); err != nil {
		t.Fatalf("write: %v", err)
	}

	buf := make([]byte, 4096)
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("read answer: %v", err)
	}
	resp := string(buf[:n])
	if !strings.Contains(resp, "200 OK") {
		t.Fatalf("expected 200 OK first:\n%s", resp)
	}
	n, err = conn.Read(buf)
	if err != nil {
		t.Fatalf("read device info: %v", err)
	}
	resp = string(buf[:n])
	if !strings.Contains(resp, "<CmdType>DeviceInfo</CmdType>") && !strings.Contains(resp, `CmdType="DeviceInfo"`) {
		t.Fatalf("expected DeviceInfo answer:\n%s", resp)
	}
}
