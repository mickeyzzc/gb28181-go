package device

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log/slog"
	"net"
	"os"
)

// buildTLSConfig assembles the SIPS client TLS configuration from the
// config's certificate fields: the CA file (GB/T 28181-2022 deployments use
// self-signed CAs whose serials are the entity IDs) pins the platform; the
// optional client pair enables mutual auth.
func buildTLSConfig(cfg Config) (*tls.Config, error) {
	tlsCfg := &tls.Config{
		InsecureSkipVerify: cfg.TLSInsecureSkipVerify, //nolint:gosec // explicit lab opt-out
	}
	if cfg.TLSCAFile != "" {
		pem, err := os.ReadFile(cfg.TLSCAFile)
		if err != nil {
			return nil, fmt.Errorf("reading TLS CA file: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("TLS CA file %s contains no certificates", cfg.TLSCAFile)
		}
		tlsCfg.RootCAs = pool
	}
	if cfg.TLSCertFile != "" || cfg.TLSKeyFile != "" {
		pair, err := tls.LoadX509KeyPair(cfg.TLSCertFile, cfg.TLSKeyFile)
		if err != nil {
			return nil, fmt.Errorf("loading TLS client certificate: %w", err)
		}
		tlsCfg.Certificates = []tls.Certificate{pair}
	}
	return tlsCfg, nil
}

// startTLSClient dials the platform over SIPS (TLS over TCP), registers the
// connection for bidirectional SIP (the platform's INVITE/BYE arrive on the
// same connection), and runs the REGISTER lifecycle over it. A dropped
// connection is re-dialed by the next re-registration cycle.
func (s *Server) startTLSClient(ctx context.Context) error {
	tlsCfg, err := buildTLSConfig(s.cfg)
	if err != nil {
		return err
	}
	addr := net.JoinHostPort(s.cfg.PlatformSIPAddress, fmt.Sprintf("%d", s.cfg.PlatformSIPPort))
	conn, err := tls.Dial("tcp", addr, tlsCfg) //nolint:gosec // config comes from buildTLSConfig
	if err != nil {
		return fmt.Errorf("dialing SIPS %s: %w", addr, err)
	}
	remote := conn.RemoteAddr().String()
	s.tcpConns.Store(remote, conn)
	s.mu.Lock()
	s.tlsRemote = remote
	s.mu.Unlock()
	slog.Info("gb28181: SIPS connection established", "remote", remote)

	// Inbound SIP over the same connection (responses feed the REGISTER
	// flow via handleResponse → regRespCh; requests dispatch normally).
	go handleTLSConnection(ctx, conn, s)
	go func() {
		<-ctx.Done()
		_ = conn.Close()
	}()

	if !s.testMode {
		if err := s.runRegisterLifecycleWith(ctx, s.channelResponseSource()); err != nil {
			slog.Warn("gb28181: initial REGISTER failed, entering listen-only mode", "error", err)
		}
	}
	return nil
}

// handleTLSConnection adapts the TLS connection to the shared Content-Length
// framed SIP reader (same wire discipline as TCP).
func handleTLSConnection(ctx context.Context, conn *tls.Conn, s *Server) {
	defer conn.Close()
	remote := conn.RemoteAddr().String()
	s.tcpConns.Store(remote, conn)
	defer s.tcpConns.Delete(remote)
	slog.Info("gb28181: TLS connection established", "remote", remote)

	reader := bufio.NewReader(conn)
	readSIPStream(ctx, reader, conn, s)
}
