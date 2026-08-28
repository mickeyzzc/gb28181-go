// Package gb28181 implements SIP digest authentication (RFC 2617)
// used by GB/T 28181 platforms — Authorization header building and
// response verification.
package device

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// DigestAuth represents a parsed Digest authentication challenge.
type DigestAuth struct {
	Realm     string // Authentication realm (e.g., "3402000000")
	Nonce     string // Server nonce
	Algorithm string // Hash algorithm: "", "MD5", or "SHA-256"
	Qop       string // Quality of protection: "" or "auth"
	Opaque    string // Opaque value
	Stale     string // Whether nonce is stale
}

// ParseChallenge parses a WWW-Authenticate header value into DigestAuth.
// Expected format: Digest realm="...", nonce="...", algorithm=MD5, ...
func ParseChallenge(wwwAuthHeader string) (DigestAuth, error) {
	result := DigestAuth{}

	// Remove "Digest " prefix
	wwwAuthHeader = strings.TrimSpace(wwwAuthHeader)
	if !strings.HasPrefix(strings.ToLower(wwwAuthHeader), "digest ") {
		return result, fmt.Errorf("not a Digest challenge: %s", wwwAuthHeader)
	}
	wwwAuthHeader = strings.TrimSpace(wwwAuthHeader[7:])

	// Parse key=value pairs
	// Matches: key="value" or key=value
	re := regexp.MustCompile(`(\w+)="?([^",]+)"?`)
	matches := re.FindAllStringSubmatch(wwwAuthHeader, -1)

	for _, match := range matches {
		if len(match) != 3 {
			continue
		}
		key := strings.ToLower(match[1])
		value := match[2]

		switch key {
		case "realm":
			result.Realm = value
		case "nonce":
			result.Nonce = value
		case "algorithm":
			result.Algorithm = value
		case "qop":
			result.Qop = value
		case "opaque":
			result.Opaque = value
		case "stale":
			result.Stale = value
		}
	}

	if result.Realm == "" || result.Nonce == "" {
		return result, fmt.Errorf("missing required Digest parameters: realm or nonce")
	}

	return result, nil
}

// ComputeResponse generates the response value for Digest authentication per RFC 2617.
//
// Parameters:
//   - username: Username (device ID in GB/T 28181)
//   - realm: Realm from challenge
//   - password: Password
//   - nonce: Nonce from challenge
//   - uri: Request-URI (e.g., "sip:3402000000")
//   - method: SIP method (e.g., "REGISTER")
//   - algorithm: Hash algorithm ("" defaults to "MD5" per RFC 2617 §3.2.1)
//   - qop: Quality of protection ("" or "auth")
//   - nc: Nonce count (8 hex digits, e.g., "00000001")
//   - cnonce: Client nonce (client-generated hex string)
//
// Returns the hex-encoded response string.
//
// Basic Digest (qop not set):
//
//	HA1 = MD5(username:realm:password)
//	HA2 = MD5(method:uri)
//	response = MD5(HA1:nonce:HA2)
//
// With qop="auth":
//
//	response = MD5(HA1:nonce:nc:cnonce:qop:HA2)
func ComputeResponse(username, realm, password, nonce, uri, method, algorithm, qop, nc, cnonce string) string {
	// Default to MD5 per RFC 2617 §3.2.1 (GB/T 28181 platforms use MD5)
	if algorithm == "" {
		algorithm = "MD5"
	}

	// Compute HA1 = hash(username:realm:password)
	ha1 := ComputeDigest(algorithm, username, realm, password)

	// Compute HA2 = hash(method:uri)
	ha2 := ComputeDigest(algorithm, method, uri)

	// Compute response per RFC 2617 §3.2.2.1:
	//   qop="auth": MD5(HA1:nonce:nc:cnonce:qop:HA2)
	//   no qop:     MD5(HA1:nonce:HA2)
	if qop == "auth" {
		return ComputeDigest(algorithm, ha1, nonce, nc, cnonce, qop, ha2)
	}
	return ComputeDigest(algorithm, ha1, nonce, ha2)
}

// BuildAuthorizationHeader builds the full Authorization header value.
//
// Parameters:
//   - authChallenge: Parsed Digest challenge from 401 response
//   - username: Username (device ID)
//   - password: Password
//   - uri: Request-URI (e.g., "sip:3402000000")
//   - method: SIP method (e.g., "REGISTER")
//
// Returns the complete Authorization header string, e.g.:
// Digest username="34020000012000000001", realm="3402000000", nonce="abc123", uri="sip:3402000000", response="..."
func BuildAuthorizationHeader(authChallenge DigestAuth, username, password, uri, method string) string {
	nc := "00000001"
	cnonce := generateCnonce()

	response := ComputeResponse(
		username, authChallenge.Realm, password, authChallenge.Nonce,
		uri, method, authChallenge.Algorithm,
		authChallenge.Qop, nc, cnonce,
	)

	var buf strings.Builder
	buf.WriteString("Digest username=\"")
	buf.WriteString(username)
	buf.WriteString("\", realm=\"")
	buf.WriteString(authChallenge.Realm)
	buf.WriteString("\", nonce=\"")
	buf.WriteString(authChallenge.Nonce)
	buf.WriteString("\", uri=\"")
	buf.WriteString(uri)
	buf.WriteString("\", response=\"")
	buf.WriteString(response)
	buf.WriteString("\"")

	if authChallenge.Algorithm != "" {
		buf.WriteString(", algorithm=")
		buf.WriteString(authChallenge.Algorithm)
	}

	if authChallenge.Qop != "" {
		buf.WriteString(", qop=")
		buf.WriteString(authChallenge.Qop)
		buf.WriteString(", nc=")
		buf.WriteString(nc)
		buf.WriteString(", cnonce=\"")
		buf.WriteString(cnonce)
		buf.WriteString("\"")
	}

	if authChallenge.Opaque != "" {
		buf.WriteString(", opaque=\"")
		buf.WriteString(authChallenge.Opaque)
		buf.WriteString("\"")
	}

	return buf.String()
}

// generateCnonce returns a client nonce as a 16-char hex string.
func generateCnonce() string {
	return fmt.Sprintf("%016x", time.Now().UnixNano())
}
