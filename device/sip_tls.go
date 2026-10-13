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
	"strconv"
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

// asyncExchangeTransportOK reports whether the async A.2.5.x exchange
// notifies (snapshot/upgrade) have a send path: the shared UDP socket
// on udp, the dialed connection on the stream transports. The inbound
// "tcp" listener mode tracks no platform connection, so it rejects.
func asyncExchangeTransportOK(transport string) bool {
	switch transport {
	case "", "udp", "tcp-out", "tls":
		return true
	default:
		return false
	}
}

// startTLSListener starts the inbound SIPS listener (platform dials the
// device over TLS). Server certificate from the configured TLS pair —
// the device's own identity cert, the same convention as the client
// pair. Connections flow through the shared framed reader (all seams).
func (s *Server) startTLSListener(ctx context.Context) error {
	if s.cfg.TLSCertFile == "" || s.cfg.TLSKeyFile == "" {
		return fmt.Errorf("device.Config Transport tls-listen requires tls_cert_file + tls_key_file (the device's own certificate)")
	}
	cert, err := tls.LoadX509KeyPair(s.cfg.TLSCertFile, s.cfg.TLSKeyFile)
	if err != nil {
		return fmt.Errorf("loading TLS server certificate: %w", err)
	}
	tlsCfg := &tls.Config{ //nolint:gosec // GB convention: self-signed device certs; platform trust is app-level
		Certificates: []tls.Certificate{cert},
	}
	addr := fmt.Sprintf(":%d", s.cfg.LocalSIPPort)
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("binding SIPS listener on %s: %w", addr, err)
	}
	s.mu.Lock()
	s.tcpListener = listener
	s.mu.Unlock()
	slog.Info("gb28181: SIPS listener started", "port", s.cfg.LocalSIPPort)

	go func() {
		<-ctx.Done()
		slog.Info("gb28181: closing SIPS listener")
		listener.Close()
	}()

	for {
		conn, err := listener.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
				slog.Warn("gb28181: SIPS accept error", "error", err)
				continue
			}
		}
		tlsConn := tls.Server(conn, tlsCfg)
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			slog.Warn("gb28181: SIPS handshake failed", "remote", conn.RemoteAddr().String(), "error", err)
			_ = conn.Close()
			continue
		}
		go handleStreamConnection(ctx, tlsConn, s)
	}
}

// isStreamTransport reports whether the server runs the outbound
// stream-client model (device dials the platform, full REGISTER/keepalive
// lifecycle over the connection).
func isStreamTransport(transport string) bool {
	return transport == "tls" || transport == "tcp-out"
}

// dialStream dials the platform for the stream transports — TLS (SIPS) or
// plain TCP — registers the connection for bidirectional SIP (the
// platform's INVITE/BYE/SUBSCRIBE arrive on the same connection), and
// starts its read goroutine. Safe to call again after a drop: the fresh
// connection replaces the stale streamRemote key.
func (s *Server) dialStream(ctx context.Context) error {
	addr := net.JoinHostPort(s.cfg.PlatformSIPAddress, strconv.Itoa(s.cfg.PlatformSIPPort))
	var conn net.Conn
	if s.cfg.Transport == "tls" {
		tlsCfg, err := buildTLSConfig(s.cfg)
		if err != nil {
			return err
		}
		raw, err := (&net.Dialer{}).DialContext(ctx, "tcp", addr)
		if err != nil {
			return fmt.Errorf("dialing SIPS %s: %w", addr, err)
		}
		// tls.Dial used to infer ServerName from the address; tls.Client
		// does not — keep that behavior so configs without an explicit
		// ServerName (or with InsecureSkipVerify) keep handshaking exactly
		// as before.
		if tlsCfg.ServerName == "" {
			inferred := tlsCfg.Clone()
			if host, _, splitErr := net.SplitHostPort(addr); splitErr == nil {
				inferred.ServerName = host
			}
			tlsCfg = inferred
		}
		tlsConn := tls.Client(raw, tlsCfg) //nolint:gosec // config comes from buildTLSConfig
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			_ = raw.Close()
			return fmt.Errorf("SIPS handshake with %s: %w", addr, err)
		}
		conn = tlsConn
	} else {
		var err error
		conn, err = (&net.Dialer{}).DialContext(ctx, "tcp", addr)
		if err != nil {
			return fmt.Errorf("dialing %s: %w", addr, err)
		}
	}

	remote := conn.RemoteAddr().String()
	s.mu.Lock()
	// A previous stream may still hold a slot under the same remote key
	// (redial after a half-dead connection): drop it so sends route to
	// the fresh connection.
	if old, loaded := s.tcpConns.LoadAndDelete(remote); loaded {
		_ = old.(net.Conn).Close()
	}
	s.tcpConns.Store(remote, conn)
	s.streamRemote = remote
	s.mu.Unlock()
	slog.Info("gb28181: stream connection established", "transport", s.cfg.Transport, "remote", remote)

	// Inbound SIP over the same connection (responses feed the REGISTER
	// flow via handleResponse → regRespCh; requests dispatch normally).
	go handleStreamConnection(ctx, conn, s)
	go func() {
		<-ctx.Done()
		_ = conn.Close()
	}()
	return nil
}

// startStreamClient dials the platform (SIPS or plain TCP) and runs the
// full REGISTER/keepalive lifecycle over the connection. A dropped
// connection is re-dialed by the re-registration cycle (ensureStreamConn).
func (s *Server) startStreamClient(ctx context.Context) error {
	if err := s.dialStream(ctx); err != nil {
		return err
	}
	if !s.testMode {
		if err := s.runRegisterLifecycleWith(ctx, s.channelResponseSource()); err != nil {
			slog.Warn("gb28181: initial REGISTER failed, entering listen-only mode", "error", err)
		}
	}
	return nil
}

// ensureStreamConn re-dials the platform when the stream connection has
// dropped (the read goroutine removes it from tcpConns on exit). Called
// before every re-registration attempt for the stream transports, so a
// platform restart (or network blip) heals on the next cycle instead of
// failing sends forever.
func (s *Server) ensureStreamConn(ctx context.Context) {
	if !isStreamTransport(s.cfg.Transport) {
		return
	}
	s.mu.Lock()
	remote := s.streamRemote
	s.mu.Unlock()
	if remote != "" {
		if _, ok := s.tcpConns.Load(remote); ok {
			return
		}
	}
	slog.Info("gb28181: stream connection lost — re-dialing", "transport", s.cfg.Transport)
	if err := s.dialStream(ctx); err != nil {
		slog.Warn("gb28181: stream re-dial failed", "error", err)
	}
}

// handleStreamConnection adapts the stream connection (TLS or plain TCP)
// to the shared Content-Length framed SIP reader.
func handleStreamConnection(ctx context.Context, conn net.Conn, s *Server) {
	defer conn.Close()
	remote := conn.RemoteAddr().String()
	s.tcpConns.Store(remote, conn)
	defer s.tcpConns.Delete(remote)
	slog.Info("gb28181: stream read loop started", "remote", remote)

	reader := bufio.NewReader(conn)
	readSIPStream(ctx, reader, conn, s)
}
