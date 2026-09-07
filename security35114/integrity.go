//go:build gb35114

// Control-signaling authentication (GB35114 §9.4): every non-REGISTER
// request carries a Date header and a Note header holding a keyed SM3
// digest over the message. The digest input is the plain concatenation
// method ‖ From ‖ To ‖ Call-ID ‖ Date ‖ VKEK ‖ body with no separators.

package security35114

import (
	"bytes"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"github.com/emmansun/gmsm/sm3"
)

// FormatDate renders the GB35114 Date header form (ISO 8601 with
// millisecond precision, UTC).
func FormatDate(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05.000")
}

// DigestPayload computes the SM3 digest over the Note-header input.
func DigestPayload(method, from, to, callID, date string, vkek []byte, body string, enc VKEKEncoding) []byte {
	var b bytes.Buffer
	b.WriteString(method)
	b.WriteString(from)
	b.WriteString(to)
	b.WriteString(callID)
	b.WriteString(date)
	if enc == VKEKBase64String {
		b.WriteString(base64.StdEncoding.EncodeToString(vkek))
	} else {
		b.Write(vkek)
	}
	b.WriteString(body)
	sum := sm3.Sum(b.Bytes())
	return sum[:]
}

// BuildNoteHeader builds the Note header for an outgoing request:
//
//	Digest nonce="<base64 SM3>",algorithm=SM3
func BuildNoteHeader(method, from, to, callID, date string, vkek []byte, body string, enc VKEKEncoding) string {
	nonce := base64.StdEncoding.EncodeToString(DigestPayload(method, from, to, callID, date, vkek, body, enc))
	return `Digest nonce="` + nonce + `",algorithm=SM3`
}

// ParseNoteHeader extracts nonce and algorithm from a Note header.
func ParseNoteHeader(note string) (nonce, algorithm string, err error) {
	scheme, rest := splitScheme(note)
	if scheme != "Digest" {
		return "", "", fmt.Errorf("not a Digest Note header: %s", note)
	}
	params := parseParams(rest)
	nonce, algorithm = params["nonce"], params["algorithm"]
	if nonce == "" || algorithm == "" {
		return "", "", fmt.Errorf("Note header missing nonce or algorithm: %s", note)
	}
	return nonce, algorithm, nil
}

// VerifyNoteHeader recomputes the digest and compares it to the received
// nonce in constant time.
func VerifyNoteHeader(note, method, from, to, callID, date string, vkek []byte, body string, enc VKEKEncoding) error {
	nonce, algorithm, err := ParseNoteHeader(note)
	if err != nil {
		return err
	}
	if !strings.EqualFold(algorithm, "SM3") {
		return fmt.Errorf("unsupported Note algorithm %q (want SM3)", algorithm)
	}
	want, err := base64.StdEncoding.DecodeString(nonce)
	if err != nil {
		return fmt.Errorf("nonce is not base64: %w", err)
	}
	got := DigestPayload(method, from, to, callID, date, vkek, body, enc)
	if subtle.ConstantTimeCompare(got, want) != 1 {
		return fmt.Errorf("Note digest mismatch")
	}
	return nil
}
