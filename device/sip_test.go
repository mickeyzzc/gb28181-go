package device

import (
	"strconv"
	"strings"
	"testing"
)

// TestRegisterSerialize tests that BuildRegister produces valid SIP REGISTER message.
func TestRegisterSerialize(t *testing.T) {
	msg := BuildRegister(
		"sip:3402000000@3402000000",
		"<sip:34020000012000000001@3402000000>;tag=12345",
		"<sip:3402000000@3402000000>",
		"1234567890@192.168.1.100",
		"1 REGISTER",
		"<sip:34020000012000000001@192.168.1.100:5060>",
		"",
	)

	data := msg.Serialize()

	// Check start line
	if !strings.Contains(string(data), "REGISTER") {
		t.Error("REGISTER message missing REGISTER method")
	}
	if !strings.Contains(string(data), "SIP/2.0") {
		t.Error("REGISTER message missing SIP/2.0")
	}

	// Check required headers
	requiredHeaders := []string{
		"From:",
		"To:",
		"Call-ID:",
		"CSeq:",
		"Contact:",
		"Expires:",
		"Max-Forwards:",
		"User-Agent:",
	}
	for _, header := range requiredHeaders {
		if !strings.Contains(string(data), header) {
			t.Errorf("REGISTER message missing %s header", header)
		}
	}

	// Check Content-Length header
	if !strings.Contains(string(data), "Content-Length: 0") {
		t.Error("REGISTER message should have Content-Length: 0")
	}
}

// TestRegisterHasRport tests that Via header includes ;rport for NAT traversal.
func TestRegisterHasRport(t *testing.T) {
	msg := BuildRegister(
		"sip:3402000000@3402000000",
		"<sip:34020000012000000001@3402000000>;tag=12345",
		"<sip:3402000000@3402000000>",
		"1234567890@192.168.1.100",
		"1 REGISTER",
		"<sip:34020000012000000001@192.168.1.100:5060>",
		"",
	)

	// Set Via header
	msg.Via = "SIP/2.0/UDP 192.168.1.100:5060;branch=z9hG4bK12345"
	data := msg.Serialize()

	// Check that rport is present (case-insensitive)
	if !strings.Contains(strings.ToLower(string(data)), ";rport") {
		t.Error("Via header should include ;rport for NAT traversal")
	}

	// Test that existing rport is preserved
	msg2 := BuildRegister(
		"sip:3402000000@3402000000",
		"<sip:34020000012000000001@3402000000>;tag=12345",
		"<sip:3402000000@3402000000>",
		"1234567890@192.168.1.100",
		"1 REGISTER",
		"<sip:34020000012000000001@192.168.1.100:5060>",
		"",
	)
	msg2.Via = "SIP/2.0/UDP 192.168.1.100:5060;RPORT;branch=z9hG4bK12345"
	data2 := msg2.Serialize()

	// Count rport occurrences (should be exactly 1)
	rportCount := strings.Count(strings.ToLower(string(data2)), ";rport")
	if rportCount != 1 {
		t.Errorf("Expected 1 ;rport in Via header, got %d", rportCount)
	}
}

// TestDigestAuth_MD5_Default tests that missing algorithm defaults to MD5.
func TestDigestAuth_MD5_Default(t *testing.T) {
	challenge := `Digest realm="3402000000", nonce="abc123"`
	auth, err := ParseChallenge(challenge)
	if err != nil {
		t.Fatalf("ParseChallenge failed: %v", err)
	}

	if auth.Algorithm != "" {
		t.Errorf("Expected empty algorithm, got %s", auth.Algorithm)
	}

	// ComputeResponse should default to MD5 when algorithm is empty
	response := ComputeResponse(
		"34020000012000000001",
		auth.Realm,
		"password123",
		auth.Nonce,
		"sip:3402000000",
		"REGISTER",
		auth.Algorithm, // Empty - should default to MD5
		"", "", "",     // No qop
	)

	// Expected MD5 response per RFC 2617 example
	// HA1 = MD5("34020000012000000001:3402000000:password123") = fc95f90497e1767b16b4d7feef3e663f
	// HA2 = MD5("REGISTER:sip:3402000000") = 060f8a9e0c4b74f7ab0c853b4580f88b
	// response = MD5(HA1:abc123:HA2) = 6fb5244bbab9ef7b862a0620a2154570
	expected := "6fb5244bbab9ef7b862a0620a2154570"
	if response != expected {
		t.Errorf("MD5 response mismatch: got %s, want %s", response, expected)
	}
}

// TestDigestAuth_SHA256_WhenRequested tests SHA-256 when explicitly requested.
func TestDigestAuth_SHA256_WhenRequested(t *testing.T) {
	challenge := `Digest realm="3402000000", nonce="abc123", algorithm=SHA-256`
	auth, err := ParseChallenge(challenge)
	if err != nil {
		t.Fatalf("ParseChallenge failed: %v", err)
	}

	if !strings.EqualFold(auth.Algorithm, "SHA-256") {
		t.Errorf("Expected algorithm SHA-256, got %s", auth.Algorithm)
	}

	// ComputeResponse should use SHA-256 when requested
	response := ComputeResponse(
		"34020000012000000001",
		auth.Realm,
		"password123",
		auth.Nonce,
		"sip:3402000000",
		"REGISTER",
		auth.Algorithm,
		"", "", "", // No qop
	)

	// SHA-256 response (different from MD5)
	md5Response := ComputeResponse(
		"34020000012000000001",
		auth.Realm,
		"password123",
		auth.Nonce,
		"sip:3402000000",
		"REGISTER",
		"MD5",
		"", "", "", // No qop
	)

	if response == md5Response {
		t.Error("SHA-256 response should differ from MD5 response")
	}

	// Verify it's a 64-character hex string (SHA-256 = 256 bits = 64 hex chars)
	if len(response) != 64 {
		t.Errorf("SHA-256 response should be 64 hex chars, got %d", len(response))
	}
}

// TestParse401Challenge tests parsing a 401 WWW-Authenticate header.
func TestParse401Challenge(t *testing.T) {
	challenge := `Digest realm="3402000000", nonce="dcd98b7102dd2f0e8b11d0f600bfb0c093", algorithm=MD5, qop=auth, opaque="5ccc069c403ebaf9f0171e9517f40e41"`
	auth, err := ParseChallenge(challenge)
	if err != nil {
		t.Fatalf("ParseChallenge failed: %v", err)
	}

	if auth.Realm != "3402000000" {
		t.Errorf("Expected realm 3402000000, got %s", auth.Realm)
	}
	if auth.Nonce != "dcd98b7102dd2f0e8b11d0f600bfb0c093" {
		t.Errorf("Expected nonce dcd98b7102dd2f0e8b11d0f600bfb0c093, got %s", auth.Nonce)
	}
	if !strings.EqualFold(auth.Algorithm, "MD5") {
		t.Errorf("Expected algorithm MD5, got %s", auth.Algorithm)
	}
	if auth.Qop != "auth" {
		t.Errorf("Expected qop auth, got %s", auth.Qop)
	}
	if auth.Opaque != "5ccc069c403ebaf9f0171e9517f40e41" {
		t.Errorf("Expected opaque 5ccc069c403ebaf9f0171e9517f40e41, got %s", auth.Opaque)
	}
}

// TestRegisterRoundtrip tests BuildRegister -> Serialize -> Parse roundtrip.
func TestRegisterRoundtrip(t *testing.T) {
	original := BuildRegister(
		"sip:3402000000@3402000000",
		"<sip:34020000012000000001@3402000000>;tag=12345",
		"<sip:3402000000@3402000000>",
		"1234567890@192.168.1.100",
		"1 REGISTER",
		"<sip:34020000012000000001@192.168.1.100:5060>",
		"",
	)
	original.Via = "SIP/2.0/UDP 192.168.1.100:5060;branch=z9hG4bK12345"

	data := original.Serialize()
	parsed, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if parsed.Method != original.Method {
		t.Errorf("Method mismatch: got %s, want %s", parsed.Method, original.Method)
	}
	if parsed.RequestURI != original.RequestURI {
		t.Errorf("RequestURI mismatch: got %s, want %s", parsed.RequestURI, original.RequestURI)
	}
	if parsed.From != original.From {
		t.Errorf("From mismatch: got %s, want %s", parsed.From, original.From)
	}
	if parsed.To != original.To {
		t.Errorf("To mismatch: got %s, want %s", parsed.To, original.To)
	}
	if parsed.CallID != original.CallID {
		t.Errorf("CallID mismatch: got %s, want %s", parsed.CallID, original.CallID)
	}
	if parsed.CSeq != original.CSeq {
		t.Errorf("CSeq mismatch: got %s, want %s", parsed.CSeq, original.CSeq)
	}
	if parsed.Contact != original.Contact {
		t.Errorf("Contact mismatch: got %s, want %s", parsed.Contact, original.Contact)
	}
	if parsed.Expires != original.Expires {
		t.Errorf("Expires mismatch: got %s, want %s", parsed.Expires, original.Expires)
	}
	if parsed.MaxForwards != original.MaxForwards {
		t.Errorf("MaxForwards mismatch: got %s, want %s", parsed.MaxForwards, original.MaxForwards)
	}
	if !strings.Contains(strings.ToLower(parsed.Via), ";rport") {
		t.Error("Parsed Via should contain ;rport")
	}
}

// TestSipParse tests parsing a REGISTER request from bytes.
func TestSipParse(t *testing.T) {
	rawSIP := "REGISTER sip:3402000000@3402000000 SIP/2.0\r\n" +
		"Via: SIP/2.0/UDP 192.168.1.100:5060;branch=z9hG4bK12345\r\n" +
		"Max-Forwards: 70\r\n" +
		"From: <sip:34020000012000000001@3402000000>;tag=12345\r\n" +
		"To: <sip:3402000000@3402000000>\r\n" +
		"Call-ID: 1234567890@192.168.1.100\r\n" +
		"CSeq: 1 REGISTER\r\n" +
		"Contact: <sip:34020000012000000001@192.168.1.100:5060>\r\n" +
		"Expires: 3600\r\n" +
		"User-Agent: MiBee-GB28181/1.0\r\n" +
		"Content-Length: 0\r\n" +
		"\r\n"

	msg, err := Parse([]byte(rawSIP))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if msg.Method != "REGISTER" {
		t.Errorf("Expected method REGISTER, got %s", msg.Method)
	}
	if msg.RequestURI != "sip:3402000000@3402000000" {
		t.Errorf("Expected RequestURI sip:3402000000@3402000000, got %s", msg.RequestURI)
	}
	if msg.From != "<sip:34020000012000000001@3402000000>;tag=12345" {
		t.Errorf("From mismatch: %s", msg.From)
	}
	if msg.To != "<sip:3402000000@3402000000>" {
		t.Errorf("To mismatch: %s", msg.To)
	}
	if msg.CallID != "1234567890@192.168.1.100" {
		t.Errorf("CallID mismatch: %s", msg.CallID)
	}
	if msg.CSeq != "1 REGISTER" {
		t.Errorf("CSeq mismatch: %s", msg.CSeq)
	}
	if msg.Expires != "3600" {
		t.Errorf("Expected Expires 3600, got %s", msg.Expires)
	}
	if msg.MaxForwards != "70" {
		t.Errorf("Expected MaxForwards 70, got %s", msg.MaxForwards)
	}
}

// TestBuild200OK tests building a 200 OK response from a request.
func TestBuild200OK(t *testing.T) {
	req := SipMessage{
		Method:     "INVITE",
		RequestURI: "sip:34020000001320000001@3402000000",
		From:       "<sip:34020000012000000001@3402000000>;tag=12345",
		To:         "<sip:34020000001320000001@3402000000>",
		CallID:     "1234567890@192.168.1.100",
		CSeq:       "1 INVITE",
		Via:        "SIP/2.0/UDP 192.168.1.100:5060;rport;branch=z9hG4bK-abc123",
		Headers:    make(map[string]string),
	}
	msg := Build200OK(req, "application/sdp", "v=0\r\no=- 12345 12345 IN IP4 192.168.1.100\r\ns=GB28181\r\nc=IN IP4 192.168.1.100\r\n")

	data := msg.Serialize()

	// Check status line
	if !strings.Contains(string(data), "SIP/2.0 200 OK") {
		t.Error("200 OK response missing 'SIP/2.0 200 OK'")
	}

	// Via MUST be copied verbatim from the request (transaction matching)
	if !strings.Contains(string(data), "Via: SIP/2.0/UDP 192.168.1.100:5060;rport;branch=z9hG4bK-abc123") {
		t.Error("200 OK response missing Via copied from request")
	}

	// From MUST echo the request From (platform), not be swapped
	if !strings.Contains(string(data), "From: <sip:34020000012000000001@3402000000>;tag=12345") {
		t.Error("200 OK response From must echo request From")
	}

	// To MUST echo the request To with a tag appended
	if !strings.Contains(string(data), "To: <sip:34020000001320000001@3402000000>;tag=") {
		t.Error("200 OK response missing To tag")
	}

	// Call-ID and CSeq preserved
	if !strings.Contains(string(data), "Call-ID: 1234567890@192.168.1.100") {
		t.Error("200 OK response missing Call-ID")
	}
	if !strings.Contains(string(data), "CSeq: 1 INVITE") {
		t.Error("200 OK response missing CSeq")
	}

	// Check body presence
	if !strings.Contains(string(data), "v=0") {
		t.Error("200 OK response missing SDP body")
	}

	// Check Content-Type
	if !strings.Contains(string(data), "Content-Type: application/sdp") {
		t.Error("200 OK response missing Content-Type: application/sdp")
	}

	// Check Content-Length
	if !strings.Contains(string(data), "Content-Length:") {
		t.Error("200 OK response missing Content-Length header")
	}
}

// TestComputeResponse_KnownValue tests RFC 2617 example values.
func TestComputeResponse_KnownValue(t *testing.T) {
	// RFC 2617 Section 3.2.2.3 example:
	// username="Mufasa", realm="testrealm@host.com", password="Circle Of Life"
	// nonce="dcd98b7102dd2f0e8b11d0f600bfb0c093"
	// uri="/dir/index.html", method="GET"
	// Expected response = "670fd8c2df070c60b045671b8b24ff02"

	// Note: RFC example uses HTTP GET with URI "/dir/index.html"
	// We test the same hash computation with SIP parameters
	response := ComputeResponse(
		"Mufasa",
		"testrealm@host.com",
		"Circle Of Life",
		"dcd98b7102dd2f0e8b11d0f600bfb0c093",
		"/dir/index.html",
		"GET",
		"",         // Empty algorithm defaults to MD5
		"", "", "", // No qop
	)

	expected := "670fd8c2df070c60b045671b8b24ff02"
	if response != expected {
		t.Errorf("ComputeResponse mismatch: got %s, want %s", response, expected)
	}
}

// TestComputeResponse_QopAuth tests the RFC 2617 §3.5 example with qop="auth".
func TestComputeResponse_QopAuth(t *testing.T) {
	// RFC 2617 Section 3.5 example:
	// username="Mufasa", realm="testrealm@host.com", password="Circle Of Life"
	// nonce="dcd98b7102dd2f0e8b11d0f600bfb0c093", uri="/dir/index.html", method="GET"
	// qop=auth, nc=00000001, cnonce="0a4f113b"
	// Expected response = "6629fae49393a05397450978507c4ef1"
	response := ComputeResponse(
		"Mufasa",
		"testrealm@host.com",
		"Circle Of Life",
		"dcd98b7102dd2f0e8b11d0f600bfb0c093",
		"/dir/index.html",
		"GET",
		"MD5",
		"auth", "00000001", "0a4f113b",
	)

	expected := "6629fae49393a05397450978507c4ef1"
	if response != expected {
		t.Errorf("ComputeResponse qop=auth mismatch: got %s, want %s", response, expected)
	}
}

// TestBuildBye tests building a BYE request.
func TestBuildBye(t *testing.T) {
	msg := BuildBye(
		"sip:3402000000@3402000000",
		"<sip:34020000012000000001@3402000000>;tag=12345",
		"<sip:3402000000@3402000000>;tag=67890",
		"1234567890@192.168.1.100",
		"2 BYE",
		"<sip:34020000012000000001@192.168.1.100:5060>",
	)

	data := msg.Serialize()

	// Check start line
	if !strings.Contains(string(data), "BYE") {
		t.Error("BYE message missing BYE method")
	}

	// Check required headers
	requiredHeaders := []string{
		"From:",
		"To:",
		"Call-ID:",
		"CSeq:",
		"Contact:",
		"Max-Forwards:",
		"User-Agent:",
	}
	for _, header := range requiredHeaders {
		if !strings.Contains(string(data), header) {
			t.Errorf("BYE message missing %s header", header)
		}
	}

	// BYE should NOT have Expires header
	if strings.Contains(string(data), "Expires:") {
		t.Error("BYE message should not have Expires header")
	}
}

// TestParseResponse tests parsing a SIP 401 response.
func TestParseResponse(t *testing.T) {
	raw401 := "SIP/2.0 401 Unauthorized\r\n" +
		"Via: SIP/2.0/UDP 192.168.1.100:5060;branch=z9hG4bK12345;rport=5060\r\n" +
		"From: <sip:34020000012000000001@3402000000>;tag=12345\r\n" +
		"To: <sip:3402000000@3402000000>;tag=67890\r\n" +
		"Call-ID: 1234567890@192.168.1.100\r\n" +
		"CSeq: 1 REGISTER\r\n" +
		"WWW-Authenticate: Digest realm=\"3402000000\", nonce=\"abc123\", algorithm=MD5\r\n" +
		"Content-Length: 0\r\n" +
		"\r\n"

	msg, err := Parse([]byte(raw401))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if msg.StatusCode != 401 {
		t.Errorf("Expected status 401, got %d", msg.StatusCode)
	}
	if msg.Method != "" {
		t.Errorf("Response should not have Method, got %s", msg.Method)
	}
	if msg.WWWAuthenticate != `Digest realm="3402000000", nonce="abc123", algorithm=MD5` {
		t.Errorf("WWW-Authenticate mismatch: %s", msg.WWWAuthenticate)
	}
}

// TestBuildAuthorizationHeader tests building the full Authorization header.
func TestBuildAuthorizationHeader(t *testing.T) {
	challenge := DigestAuth{
		Realm:     "3402000000",
		Nonce:     "abc123",
		Algorithm: "MD5",
	}

	authHeader := BuildAuthorizationHeader(
		challenge,
		"34020000012000000001",
		"password123",
		"sip:3402000000",
		"REGISTER",
	)

	// Check required components
	required := []string{
		`username="34020000012000000001"`,
		`realm="3402000000"`,
		`nonce="abc123"`,
		`uri="sip:3402000000"`,
		`response="`,
		`algorithm=MD5`,
	}
	for _, comp := range required {
		if !strings.Contains(authHeader, comp) {
			t.Errorf("Authorization header missing %s: %s", comp, authHeader)
		}
	}

	if !strings.HasPrefix(authHeader, "Digest ") {
		t.Error("Authorization header should start with 'Digest '")
	}
}

// TestBuildAuthorizationHeader_QopAuth verifies nc and cnonce are included when qop is set.
func TestBuildAuthorizationHeader_QopAuth(t *testing.T) {
	challenge := DigestAuth{
		Realm:     "3402000000",
		Nonce:     "abc123",
		Algorithm: "MD5",
		Qop:       "auth",
	}

	authHeader := BuildAuthorizationHeader(
		challenge,
		"34020000012000000001",
		"password123",
		"sip:3402000000",
		"REGISTER",
	)

	// qop, nc, and cnonce must be present for the NVR to accept the digest
	required := []string{
		`qop=auth`,
		`nc=00000001`,
		`cnonce="`,
	}
	for _, comp := range required {
		if !strings.Contains(authHeader, comp) {
			t.Errorf("Authorization header missing %s: %s", comp, authHeader)
		}
	}

	// cnonce must be a 16-char hex string
	idx := strings.Index(authHeader, `cnonce="`)
	if idx < 0 {
		t.Fatal("cnonce not found in Authorization header")
	}
	cnonce := authHeader[idx+len(`cnonce="`) : idx+len(`cnonce="`)+16]
	if _, err := strconv.ParseUint(cnonce, 16, 64); err != nil {
		t.Errorf("cnonce %q is not hex: %v", cnonce, err)
	}
}

// TestComputeDigest tests the low-level digest computation function.
func TestComputeDigest(t *testing.T) {
	// Test MD5
	md5Result := ComputeDigest("MD5", "hello", "world")
	if md5Result != "6de41d334b7ce946682da48776a10bb9" {
		t.Errorf("MD5 digest mismatch: got %s", md5Result)
	}

	// Test SHA-256
	sha256Result := ComputeDigest("SHA-256", "hello", "world")
	if len(sha256Result) != 64 {
		t.Errorf("SHA-256 digest should be 64 chars, got %d", len(sha256Result))
	}

	// Test default (empty algorithm = MD5)
	defaultResult := ComputeDigest("", "hello", "world")
	if defaultResult != md5Result {
		t.Error("Empty algorithm should default to MD5")
	}
}
