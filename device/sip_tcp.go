package device

import (
	"bufio"
	"context"
	"fmt"
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

	reader := bufio.NewReader(conn)

	for {
		select {
		case <-ctx.Done():
			return
		default:
			// Set read deadline for shutdown responsiveness
			conn.SetReadDeadline(time.Now().Add(1 * time.Second))

			// Read headers until empty line
			var headers []string
			var contentLength int
			for {
				line, err := reader.ReadString('\n')
				if err != nil {
					if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
						continue // Timeout is expected for shutdown check
					}
					slog.Warn("gb28181: TCP connection read error", "error", err)
					return
				}
				line = strings.TrimRight(line, "\r\n")
				headers = append(headers, line)
				if line == "" {
					break
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

			// Read body if Content-Length > 0
			if contentLength > 0 {
				body := make([]byte, contentLength)
				_, err := reader.Read(body)
				if err != nil {
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

			// Get TCP address for dispatch
			tcpAddr := conn.RemoteAddr().(*net.TCPAddr)

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
				slog.Info("gb28181: received method, responding 200 OK", "method", msg.Method, "from", remoteAddr)
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
