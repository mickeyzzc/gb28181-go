// implements the GB/T 28181 SIP signaling protocol
// (RFC 3261 subset) by hand — register, invite, keepalive, and bye message
// parsing/building with no external SIP library.

package device

import (
	"bytes"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// SipMessage represents a SIP message (request or response).
type SipMessage struct {
	// Request fields
	Method     string // REGISTER, INVITE, ACK, BYE, MESSAGE, CANCEL, OPTIONS
	StatusCode int    // For responses: 200, 401, etc.
	RequestURI string // e.g., "sip:3402000000@3402000000"

	// Core headers (RFC 3261)
	From    string // e.g., "<sip:34020000012000000001@3402000000>;tag=12345"
	To      string // e.g., "<sip:3402000000@3402000000>;tag=67890"
	CallID  string // e.g., "1234567890@192.168.1.100"
	CSeq    string // e.g., "1 REGISTER"
	Contact string // e.g., "<sip:34020000012000000001@192.168.1.100:5060>"
	Via     string // e.g., "SIP/2.0/UDP 192.168.1.100:5060;rport;branch=z9hG4bK..."
	Expires string // e.g., "3600"

	// Additional headers
	MaxForwards     string
	ContentType     string
	ContentLength   int
	WWWAuthenticate string // For 401 responses
	Authorization   string // For authenticated requests
	UserAgent       string

	// Extension headers (case-insensitive lookup)
	Headers map[string]string

	// Body
	Body string
}

// Serialize converts the SipMessage to its wire format.
// Returns bytes in \r\n line ending format per RFC 3261.
func (m *SipMessage) Serialize() []byte {
	var buf bytes.Buffer

	// Write start line
	if m.Method != "" {
		// Request: METHOD request_uri SIP/2.0
		buf.WriteString(m.Method)
		buf.WriteByte(' ')
		buf.WriteString(m.RequestURI)
		buf.WriteString(" SIP/2.0\r\n")
	} else if m.StatusCode > 0 {
		// Response: SIP/2.0 statusCode reason
		buf.WriteString("SIP/2.0 ")
		buf.WriteString(strconv.Itoa(m.StatusCode))
		buf.WriteByte(' ')
		buf.WriteString(statusReason(m.StatusCode))
		buf.WriteString("\r\n")
	} else {
		// Invalid: neither request nor response
		buf.WriteString("SIP/2.0 500 Invalid Message\r\n")
	}

	// Write Via header (if present, ensures rport)
	if m.Via != "" {
		buf.WriteString("Via: ")
		if !strings.Contains(strings.ToLower(m.Via), ";rport") {
			buf.WriteString(strings.TrimRight(m.Via, "\r\n"))
			buf.WriteString(";rport\r\n")
		} else {
			buf.WriteString(strings.TrimRight(m.Via, "\r\n"))
			buf.WriteString("\r\n")
		}
	}

	// Write headers in canonical order
	if m.MaxForwards != "" {
		buf.WriteString("Max-Forwards: ")
		buf.WriteString(m.MaxForwards)
		buf.WriteString("\r\n")
	}
	if m.From != "" {
		buf.WriteString("From: ")
		buf.WriteString(m.From)
		buf.WriteString("\r\n")
	}
	if m.To != "" {
		buf.WriteString("To: ")
		buf.WriteString(m.To)
		buf.WriteString("\r\n")
	}
	if m.CallID != "" {
		buf.WriteString("Call-ID: ")
		buf.WriteString(m.CallID)
		buf.WriteString("\r\n")
	}
	if m.CSeq != "" {
		buf.WriteString("CSeq: ")
		buf.WriteString(m.CSeq)
		buf.WriteString("\r\n")
	}
	if m.Contact != "" {
		buf.WriteString("Contact: ")
		buf.WriteString(m.Contact)
		buf.WriteString("\r\n")
	}
	if m.Expires != "" {
		buf.WriteString("Expires: ")
		buf.WriteString(m.Expires)
		buf.WriteString("\r\n")
	}
	if m.WWWAuthenticate != "" {
		buf.WriteString("WWW-Authenticate: ")
		buf.WriteString(m.WWWAuthenticate)
		buf.WriteString("\r\n")
	}
	if m.Authorization != "" {
		buf.WriteString("Authorization: ")
		buf.WriteString(m.Authorization)
		buf.WriteString("\r\n")
	}
	if m.UserAgent != "" {
		buf.WriteString("User-Agent: ")
		buf.WriteString(m.UserAgent)
		buf.WriteString("\r\n")
	}
	if m.ContentType != "" {
		buf.WriteString("Content-Type: ")
		buf.WriteString(m.ContentType)
		buf.WriteString("\r\n")
	}

	// Write extension headers
	for k, v := range m.Headers {
		buf.WriteString(k)
		buf.WriteString(": ")
		buf.WriteString(v)
		buf.WriteString("\r\n")
	}

	// Write Content-Length
	bodyLen := len(m.Body)
	if m.ContentLength > 0 {
		bodyLen = m.ContentLength
	}
	buf.WriteString("Content-Length: ")
	buf.WriteString(strconv.Itoa(bodyLen))
	buf.WriteString("\r\n")

	// Empty line separator
	buf.WriteString("\r\n")

	// Write body if present
	if m.Body != "" {
		buf.WriteString(m.Body)
	}

	return buf.Bytes()
}

// Parse parses a SIP message from bytes.
// Supports both requests and responses per RFC 3261.
func Parse(data []byte) (SipMessage, error) {
	msg := SipMessage{
		Headers:     make(map[string]string),
		MaxForwards: "70", // Default per RFC 3261
	}
	lines := strings.Split(string(data), "\r\n")
	if len(lines) < 1 {
		return msg, errors.New("empty SIP message")
	}

	// Parse start line
	startLine := strings.TrimSpace(lines[0])
	if strings.HasPrefix(startLine, "SIP/2.0") {
		// Response: SIP/2.0 StatusCode Reason
		parts := strings.SplitN(startLine, " ", 3)
		if len(parts) >= 2 {
			statusCode, err := strconv.Atoi(parts[1])
			if err != nil {
				return msg, fmt.Errorf("invalid status code: %w", err)
			}
			msg.StatusCode = statusCode
		}
	} else {
		// Request: METHOD RequestURI SIP/2.0
		parts := strings.SplitN(startLine, " ", 3)
		if len(parts) >= 3 && parts[2] == "SIP/2.0" {
			msg.Method = parts[0]
			msg.RequestURI = parts[1]
		} else {
			return msg, fmt.Errorf("invalid SIP request line: %s", startLine)
		}
	}

	// Parse headers until empty line
	bodyStart := -1
	for i := 1; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			bodyStart = i + 1
			break
		}
		// Split header name: value
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue // Skip malformed headers
		}
		name := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])

		// Parse standard headers
		switch strings.ToLower(name) {
		case "via":
			msg.Via = value
		case "from":
			msg.From = value
		case "to":
			msg.To = value
		case "call-id", "callid":
			msg.CallID = value
		case "cseq":
			msg.CSeq = value
		case "contact":
			msg.Contact = value
		case "expires":
			msg.Expires = value
		case "max-forwards":
			msg.MaxForwards = value
		case "content-type":
			msg.ContentType = value
		case "content-length":
			if n, err := strconv.Atoi(value); err == nil {
				msg.ContentLength = n
			}
		case "www-authenticate":
			msg.WWWAuthenticate = value
		case "authorization":
			msg.Authorization = value
		case "user-agent":
			msg.UserAgent = value
		default:
			msg.Headers[name] = value
		}
	}

	// Extract body
	if bodyStart >= 0 && bodyStart < len(lines) {
		msg.Body = strings.Join(lines[bodyStart:], "\r\n")
	}

	return msg, nil
}

// BuildRegister creates a REGISTER request.
// If authHeader is non-empty, it adds the Authorization header.
func BuildRegister(requestUri, from, to, callId, cseq, contact, authHeader string) SipMessage {
	msg := SipMessage{
		Method:      "REGISTER",
		RequestURI:  requestUri,
		From:        from,
		To:          to,
		CallID:      callId,
		CSeq:        cseq,
		Contact:     contact,
		MaxForwards: "70",
		Expires:     "3600",
		UserAgent:   UserAgent,
		Headers:     make(map[string]string),
	}
	if authHeader != "" {
		msg.Authorization = authHeader
	}
	return msg
}

// BuildBye creates a BYE request for terminating a dialog.
func BuildBye(requestUri, from, to, callId, cseq, contact string) SipMessage {
	return SipMessage{
		Method:      "BYE",
		RequestURI:  requestUri,
		From:        from,
		To:          to,
		CallID:      callId,
		CSeq:        cseq,
		Contact:     contact,
		MaxForwards: "70",
		UserAgent:   UserAgent,
		Headers:     make(map[string]string),
	}
}

// dialogTag is a stable To-tag suffix for this process. A single stable tag
// is sufficient for a single-dialog GB28181 device. It is drawn from
// crypto/rand — To-tags must not be predictable across restarts (RFC 3261
// §19.3), and it carries no product prefix.
var dialogTag = randomTag()

// randomTag returns 8 lowercase hex chars from crypto/rand, falling back to
// the clock only if the system CSPRNG is unavailable.
func randomTag() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%08x", time.Now().UnixNano()&0xFFFFFFFF)
	}
	return hex.EncodeToString(b[:])
}

// Build200OK creates a 200 OK response to an incoming request.
// Per RFC 3261 §8.2.6.2 it copies the request Via verbatim (required for
// transaction matching — responses without Via are dropped by every SIP
// stack), echoes From/To, appends a To tag if absent, and preserves
// Call-ID/CSeq.
func Build200OK(req SipMessage, contentType, body string) SipMessage {
	to := req.To
	if !strings.Contains(to, "tag=") {
		to = to + ";tag=" + dialogTag
	}
	return SipMessage{
		StatusCode:  200,
		Via:         req.Via,
		From:        req.From,
		To:          to,
		CallID:      req.CallID,
		CSeq:        req.CSeq,
		ContentType: contentType,
		Body:        body,
		UserAgent:   UserAgent,
		Headers:     make(map[string]string),
	}
}

// statusReason returns the reason phrase for a SIP status code.
func statusReason(code int) string {
	switch code {
	case 100:
		return "Trying"
	case 200:
		return "OK"
	case 401:
		return "Unauthorized"
	case 404:
		return "Not Found"
	case 488:
		return "Not Acceptable Here"
	case 500:
		return "Server Internal Error"
	case 503:
		return "Service Unavailable"
	default:
		return "Unknown"
	}
}

// computeDigest computes a digest hash (MD5 or SHA-256).
// It concatenates all inputs with colons and returns hex-encoded hash.
// Helper for Digest auth; exported for testing.
func ComputeDigest(algorithm string, inputs ...string) string {
	// Default to MD5 per RFC 2617 §3.2.1
	if algorithm == "" {
		algorithm = "MD5"
	}

	var data []byte
	for i, s := range inputs {
		if i > 0 {
			data = append(data, ':')
		}
		data = append(data, s...)
	}

	switch strings.ToUpper(algorithm) {
	case "MD5":
		hash := md5.Sum(data)
		return hex.EncodeToString(hash[:])
	case "SHA-256", "SHA256":
		hash := sha256.Sum256(data)
		return hex.EncodeToString(hash[:])
	default:
		// Fallback to MD5 for unknown algorithms
		hash := md5.Sum(data)
		return hex.EncodeToString(hash[:])
	}
}
