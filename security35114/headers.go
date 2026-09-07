//go:build gb35114

// GB35114 A-level SIP header construction and parsing. The three
// authentication headers are:
//
//   - Authorization: Capability/Unidirection/Bidirection (device → platform)
//   - WWW-Authenticate: Unidirection/Bidirection challenge (401 response)
//   - SecurityInfo: Unidirection/Bidirection result (200 OK response)

package security35114

import (
	"encoding/base64"
	"fmt"
	"regexp"
	"strings"
)

// Mode is the GB35114 authentication scheme negotiated by the platform.
type Mode string

const (
	// ModeUnidirection authenticates the device to the platform only.
	ModeUnidirection Mode = "Unidirection"
	// ModeBidirection additionally authenticates the platform to the device.
	ModeBidirection Mode = "Bidirection"
)

// Challenge is the parsed WWW-Authenticate header of a GB35114 401 response.
type Challenge struct {
	Mode      Mode
	Algorithm string
	Random1   string // base64-encoded 128-bit server random
}

// SecurityInfo is the parsed SecurityInfo header of the GB35114 200 OK.
type SecurityInfo struct {
	Mode      Mode
	Algorithm string
	Random1   string // base64, echoed (bidirection)
	Random2   string // base64, echoed (bidirection)
	DeviceID  string // echoed (bidirection)
	ServerID  string // echoed (bidirection)
	CryptKey  string // base64 DER SM2 envelope carrying the VKEK
	Sign2     string // base64 DER SM2 signature (bidirection)
}

// paramPattern matches key="value" and key=value pairs inside the
// authentication headers. Values never contain '"' or ','.
var paramPattern = regexp.MustCompile(`(\w+)="([^"]*)"|(\w+)=([^",]+)`)

// parseParams splits a scheme-stripped header value into its parameters.
// Quoted and bare values are both accepted (Authorization quotes,
// SecurityInfo captures show the same, the Note header uses bare
// algorithm=SM3).
func parseParams(s string) map[string]string {
	params := make(map[string]string)
	for _, m := range paramPattern.FindAllStringSubmatch(s, -1) {
		if m[1] != "" {
			params[strings.ToLower(m[1])] = m[2]
		} else if m[3] != "" {
			params[strings.ToLower(m[3])] = m[4]
		}
	}
	return params
}

// BuildCapabilityAuthorization builds the Authorization header of the first
// REGISTER, announcing the device's security capability. When certPEM is
// non-empty the certificate is attached as cnonce="devicecert:<base64 of
// PEM>" — carried by some platforms that do not pre-provision the device
// certificate; the standard flow relies on pre-provisioned certificates and
// omits it.
func BuildCapabilityAuthorization(keyVersion, certPEM string) string {
	var b strings.Builder
	b.WriteString(`Capability algorithm="`)
	b.WriteString(CapabilityAlgorithm)
	b.WriteString(`", keyversion="`)
	b.WriteString(keyVersion)
	b.WriteString(`"`)
	if certPEM != "" {
		b.WriteString(`, cnonce="devicecert:`)
		b.WriteString(base64.StdEncoding.EncodeToString([]byte(certPEM)))
		b.WriteString(`"`)
	}
	return b.String()
}

// ParseChallenge parses the WWW-Authenticate header of a GB35114 401
// response. Both Unidirection and Bidirection challenges carry random1.
func ParseChallenge(wwwAuthenticate string) (Challenge, error) {
	scheme, rest := splitScheme(wwwAuthenticate)
	var mode Mode
	switch scheme {
	case "Unidirection":
		mode = ModeUnidirection
	case "Bidirection":
		mode = ModeBidirection
	default:
		return Challenge{}, fmt.Errorf("not a GB35114 challenge: %s", wwwAuthenticate)
	}
	params := parseParams(rest)
	ch := Challenge{
		Mode:      mode,
		Algorithm: params["algorithm"],
		Random1:   params["random1"],
	}
	if ch.Random1 == "" {
		return Challenge{}, fmt.Errorf("%s challenge missing random1", scheme)
	}
	return ch, nil
}

// BuildAuthAuthorization builds the Authorization header of the
// authenticated REGISTER. serverID is the SIP server ID from the challenge
// domain; deviceID is only emitted for Bidirection.
func BuildAuthAuthorization(ch Challenge, random2, serverID, deviceID, sign1 string) string {
	mode := ch.Mode
	if mode == "" {
		mode = ModeUnidirection
	}
	var b strings.Builder
	b.WriteString(string(mode))
	b.WriteString(` random1="`)
	b.WriteString(ch.Random1)
	b.WriteString(`", random2="`)
	b.WriteString(random2)
	b.WriteString(`", serverid="`)
	b.WriteString(serverID)
	b.WriteString(`"`)
	if mode == ModeBidirection {
		b.WriteString(`, deviceid="`)
		b.WriteString(deviceID)
		b.WriteString(`"`)
	}
	b.WriteString(`, sign1="`)
	b.WriteString(sign1)
	b.WriteString(`", algorithm="`)
	b.WriteString(CapabilityAlgorithm)
	b.WriteString(`"`)
	return b.String()
}

// AuthAuthorization is the parsed Authorization header of a GB35114
// authenticated REGISTER (platform side).
type AuthAuthorization struct {
	Mode      Mode
	Random1   string
	Random2   string
	ServerID  string
	DeviceID  string // Bidirection only
	Sign1     string
	Algorithm string
}

// ParseAuthAuthorization parses the Authorization header produced for the
// authenticated REGISTER.
func ParseAuthAuthorization(authorization string) (AuthAuthorization, error) {
	scheme, rest := splitScheme(authorization)
	var aa AuthAuthorization
	switch scheme {
	case "Unidirection":
		aa.Mode = ModeUnidirection
	case "Bidirection":
		aa.Mode = ModeBidirection
	default:
		return AuthAuthorization{}, fmt.Errorf("not a GB35114 authenticated REGISTER: %s", authorization)
	}
	params := parseParams(rest)
	aa.Random1 = params["random1"]
	aa.Random2 = params["random2"]
	aa.ServerID = params["serverid"]
	aa.DeviceID = params["deviceid"]
	aa.Sign1 = params["sign1"]
	aa.Algorithm = params["algorithm"]
	if aa.Random1 == "" || aa.Random2 == "" || aa.ServerID == "" || aa.Sign1 == "" {
		return AuthAuthorization{}, fmt.Errorf("%s Authorization missing fields (random1, random2, serverid, sign1)", scheme)
	}
	if aa.Mode == ModeBidirection && aa.DeviceID == "" {
		return AuthAuthorization{}, fmt.Errorf("Bidirection Authorization missing deviceid")
	}
	return aa, nil
}

// ParseSecurityInfo parses the SecurityInfo header of the GB35114 200 OK.
// Unidirection requires cryptkey; Bidirection additionally requires
// random1, random2, deviceid, serverid and sign2.
func ParseSecurityInfo(header string) (SecurityInfo, error) {
	scheme, rest := splitScheme(header)
	var si SecurityInfo
	switch scheme {
	case "Unidirection":
		si.Mode = ModeUnidirection
	case "Bidirection":
		si.Mode = ModeBidirection
	default:
		return SecurityInfo{}, fmt.Errorf("not a GB35114 SecurityInfo: %s", header)
	}
	params := parseParams(rest)
	si.Algorithm = params["algorithm"]
	si.Random1 = params["random1"]
	si.Random2 = params["random2"]
	si.DeviceID = params["deviceid"]
	si.ServerID = params["serverid"]
	si.CryptKey = params["cryptkey"]
	si.Sign2 = params["sign2"]
	if si.CryptKey == "" {
		return SecurityInfo{}, fmt.Errorf("%s SecurityInfo missing cryptkey", scheme)
	}
	if si.Mode == ModeBidirection && (si.Random1 == "" || si.Random2 == "" ||
		si.DeviceID == "" || si.ServerID == "" || si.Sign2 == "") {
		return SecurityInfo{}, fmt.Errorf("Bidirection SecurityInfo missing fields (random1, random2, deviceid, serverid, sign2)")
	}
	return si, nil
}

// BuildSecurityInfo renders a SecurityInfo header value (platform side of
// the handshake; used by conformance tests and UAS implementations).
// Bidirection emits the capture-observed comma-separated form.
func BuildSecurityInfo(si SecurityInfo) string {
	var b strings.Builder
	if si.Mode == ModeUnidirection {
		b.WriteString(`Unidirection cryptkey="`)
		b.WriteString(si.CryptKey)
		b.WriteString(`", algorithm="`)
		b.WriteString(si.Algorithm)
		b.WriteString(`"`)
		return b.String()
	}
	b.WriteString(`Bidirection algorithm="`)
	b.WriteString(si.Algorithm)
	b.WriteString(`",random1="`)
	b.WriteString(si.Random1)
	b.WriteString(`",random2="`)
	b.WriteString(si.Random2)
	b.WriteString(`",deviceid="`)
	b.WriteString(si.DeviceID)
	b.WriteString(`",serverid="`)
	b.WriteString(si.ServerID)
	b.WriteString(`",cryptkey="`)
	b.WriteString(si.CryptKey)
	b.WriteString(`",sign2="`)
	b.WriteString(si.Sign2)
	b.WriteString(`"`)
	return b.String()
}

// splitScheme separates "Scheme rest-of-header".
func splitScheme(header string) (string, string) {
	header = strings.TrimSpace(header)
	i := strings.IndexByte(header, ' ')
	if i < 0 {
		return header, ""
	}
	return header[:i], strings.TrimSpace(header[i+1:])
}
