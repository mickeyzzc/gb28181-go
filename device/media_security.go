// GB35114 B/C media security seams (tag-free). The gb35114-tagged
// security35114 package implements these; INVITE handling consults the
// seams so default builds carry zero national-cryptography code.
package device

import "strings"

// RTPPayloadCryptor transforms one RTP payload (encrypt or decrypt — the
// same XOR construction); ssrc and seq bind the cryptor to the packet.
type RTPPayloadCryptor = func(payload []byte, ssrc uint32, seq uint16) []byte

// AUSignerFunc signs one reassembled PS access unit (GB35114 level B).
type AUSignerFunc = func(au []byte, ssrc, timestamp uint32) ([]byte, error)

// MediaSecurityResponder answers a GB35114 "a=security:" INVITE offer.
// AnswerMediaSecurity returns the SDP answer attribute value ("" keeps the
// session plaintext) and, at level C, the RTP payload encryptor.
type MediaSecurityResponder interface {
	AnswerMediaSecurity(offerValue string) (answerValue string, encrypt RTPPayloadCryptor, err error)
}

// AUVerifierFunc verifies a level-B AU signature; a non-nil error drops the
// access unit (fail closed).
type AUVerifierFunc = func(au []byte, ssrc, timestamp uint32, sig []byte) error

// SDPSecurityAttributeValue extracts the value of the first "a=security:"
// attribute from an SDP body, or "" when absent.
func SDPSecurityAttributeValue(sdp string) string {
	for _, line := range strings.Split(sdp, "\n") {
		line = strings.TrimRight(line, "\r")
		if v, ok := strings.CutPrefix(line, "a=security:"); ok {
			if v = strings.TrimSpace(v); v != "" {
				return v
			}
		}
	}
	return ""
}
