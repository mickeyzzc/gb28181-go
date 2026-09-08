package device

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"time"
)

// startTCPListener starts the TCP SIP listener and accepts connections.
func (s *Server) startTCPListener(ctx context.Context) error {
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", s.cfg.LocalSIPPort))
	if err != nil {
		return fmt.Errorf("binding SIP TCP on port %d: %w", s.cfg.LocalSIPPort, err)
	}
	s.mu.Lock()
	s.tcpListener = listener
	s.mu.Unlock()
	slog.Info("gb28181: SIP TCP listener started", "port", s.cfg.LocalSIPPort)

	// Accept connections in a goroutine
	go func() {
		<-ctx.Done()
		slog.Info("gb28181: closing TCP listener")
		listener.Close()
	}()

	for {
		conn, err := listener.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
				slog.Warn("gb28181: TCP accept error", "error", err)
				continue
			}
		}
		go handleTCPConnection(ctx, conn, s)
	}
}

// handleTCPConnection handles a single TCP connection, reading SIP messages
// framed by Content-Length and dispatching them to the appropriate handlers.
func handleTCPConnection(ctx context.Context, conn net.Conn, s *Server) {
	defer conn.Close()

	remoteAddr := conn.RemoteAddr().String()
	slog.Info("gb28181: TCP connection established", "remote", remoteAddr)

	// Register connection in tcpConns map
	s.tcpConns.Store(remoteAddr, conn)
	defer s.tcpConns.Delete(remoteAddr)

	readSIPStream(ctx, bufio.NewReader(conn), conn, s)
}

// defaultMaxSIPMessageSize is the safe bound applied when
// Config.MaxSIPMessageSize is unset: ample for any real SIP/MANSCDP
// message (typical REGISTER < 1 KiB, catalog answers < 64 KiB), while a
// forged Content-Length can no longer drive an unbounded allocation.
const defaultMaxSIPMessageSize = 1 << 20 // 1 MiB

// sipMessageLimit resolves the effective per-message byte bound. Negative
// disables the limit (tests only).
func (s *Server) sipMessageLimit() int {
	if s.cfg.MaxSIPMessageSize < 0 {
		return 0 // unlimited
	}
	if s.cfg.MaxSIPMessageSize == 0 {
		return defaultMaxSIPMessageSize
	}
	return s.cfg.MaxSIPMessageSize
}

// readSIPStream reads Content-Length framed SIP messages from reader and
// dispatches them; replies go back over conn (looked up by remote address).
// Shared by the TCP listener and the SIPS client.
//
// Framing abuse — a forged Content-Length, or endless header lines — drops
// the connection instead of allocating (issue #37). Bodies are read with
// io.ReadFull so a short body can never be dispatched as a truncated
// message.
func readSIPStream(ctx context.Context, reader *bufio.Reader, conn net.Conn, s *Server) {
	limit := s.sipMessageLimit()
	for {
		select {
		case <-ctx.Done():
			return
		default:
			// Set read deadline for shutdown responsiveness
			conn.SetReadDeadline(time.Now().Add(1 * time.Second))

			// Read headers until empty line, bounded by the message
			// limit — a header flood without the terminating empty
			// line must not accumulate unboundedly.
			var headers []string
			var headerBytes int
			var contentLength int
		readHeaders:
			for {
				line, err := reader.ReadString('\n')
				if err != nil {
					var netErr net.Error
					if errors.As(err, &netErr) && netErr.Timeout() {
						if len(headers) == 0 {
							continue // Idle timeout between messages is expected
						}
						// Mid-headers stall: the 1s shutdown poll would
						// loop forever; treat as a dead peer.
						slog.Warn("gb28181: TCP header read stalled", "remote", conn.RemoteAddr().String())
						return
					}
					slog.Warn("gb28181: TCP connection read error", "error", err)
					return
				}
				line = strings.TrimRight(line, "\r\n")
				headerBytes += len(line) + 2
				if limit > 0 && headerBytes > limit {
					slog.Warn("gb28181: SIP header section exceeds message limit, dropping connection",
						"remote", conn.RemoteAddr().String(), "header_bytes", headerBytes, "limit", limit)
					return
				}
				headers = append(headers, line)
				if line == "" {
					break readHeaders
				}
				// Parse Content-Length header
				if strings.HasPrefix(strings.ToLower(line), "content-length:") {
					parts := strings.SplitN(line, ":", 2)
					if len(parts) == 2 {
						if n, err := strconv.Atoi(strings.TrimSpace(parts[1])); err == nil {
							contentLength = n
						}
					}
				}
			}

			// Reconstruct full message from headers
			fullMsg := strings.Join(headers, "\r\n")
			fullMsg += "\r\n"

			// Read body if Content-Length > 0. The declared length is
			// validated against the message limit BEFORE allocating, and
			// io.ReadFull guarantees a short body never becomes a
			// truncated-but-dispatched message.
			if contentLength > 0 {
				if limit > 0 && contentLength > limit {
					slog.Warn("gb28181: SIP Content-Length exceeds message limit, dropping connection",
						"remote", conn.RemoteAddr().String(), "content_length", contentLength, "limit", limit)
					return
				}
				body := make([]byte, contentLength)
				if _, err := io.ReadFull(reader, body); err != nil {
					var netErr net.Error
					if errors.As(err, &netErr) && netErr.Timeout() {
						// The rolling 1s shutdown deadline expired while
						// waiting for the promised body bytes — keep the
						// connection only if more bytes arrive; a peer that
						// stalls mid-body desyncs the stream, so drop it.
						slog.Warn("gb28181: SIP body read stalled, dropping connection",
							"remote", conn.RemoteAddr().String(), "want", contentLength)
						return
					}
					slog.Warn("gb28181: TCP body read error", "error", err)
					return
				}
				fullMsg += string(body)
			}

			// Parse SIP message
			msg, err := Parse([]byte(fullMsg))
			if err != nil {
				slog.Warn("gb28181: failed to parse SIP message", "error", err)
				continue
			}

			// Get TCP address for dispatch. Non-TCP peers (net.Pipe in
			// tests) fall back to an unspecified address.
			tcpAddr := &net.TCPAddr{}
			if peer, ok := conn.RemoteAddr().(*net.TCPAddr); ok {
				tcpAddr = peer
			}

			// Handle responses vs requests separately
			if msg.StatusCode > 0 {
				s.handleResponse(msg)
				continue
			}

			// Handle based on method
			switch msg.Method {
			case "INVITE":
				s.handleInvite(ctx, msg, tcpAddr)
			case "BYE":
				s.handleBye(ctx, msg, tcpAddr)
			case "MESSAGE":
				s.handleMessage(ctx, msg, tcpAddr)
			case "ACK":
				// No action needed - media is now flowing
			case "INFO":
				s.handleInfo(ctx, msg, tcpAddr)
			case "SUBSCRIBE", "NOTIFY", "OPTIONS":
				slog.Info("gb28181: received method, responding 200 OK", "method", msg.Method, "from", conn.RemoteAddr().String())
				ok200 := Build200OK(msg, "", "")
				if err := s.sendSIP(ok200.Serialize(), tcpAddr); err != nil {
					slog.Warn("gb28181: failed to send 200 OK", "method", msg.Method, "error", err)
				}
			default:
				slog.Debug("gb28181: unhandled SIP method", "method", msg.Method)
			}
		}
	}
}

// sendToTCP sends data to a specific TCP connection.
func (s *Server) sendToTCP(data []byte, addr *net.TCPAddr) error {
	if conn, ok := s.tcpConns.Load(addr.String()); ok {
		_, err := conn.(net.Conn).Write(data)
		return err
	}
	return fmt.Errorf("no TCP connection for %s", addr.String())
}
