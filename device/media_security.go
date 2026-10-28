// GB35114 B/C media security seams (tag-free). The gb35114-tagged
// security35114 package implements these; INVITE handling consults the
// seams so default builds carry zero national-cryptography code.
package device

import "strings"

// RTPCryptor applies the GB35114 level-C per-packet payload protection
// (SM4-OFB). Zero wipes key material at session end.
type RTPCryptor interface {
	Crypt(payload []byte, ssrc uint32, seq uint16) []byte
	Zero()
}

// AUSignerFunc signs one reassembled PS access unit (GB35114 level B).
type AUSignerFunc = func(au []byte, ssrc, timestamp uint32) ([]byte, error)

// AUVerifierFunc verifies a level-B AU signature; a non-nil error drops the
// access unit (fail closed).
type AUVerifierFunc = func(au []byte, ssrc, timestamp uint32, sig []byte) error

// MediaSecurityResponder answers a GB35114 "a=security:" INVITE offer for
// device-sent media (live, playback). AnswerMediaSecurity returns the SDP
// answer attribute value ("" keeps the session plaintext) and, at level C,
// the RTP payload encryptor.
type MediaSecurityResponder interface {
	AnswerMediaSecurity(offerValue string) (answerValue string, encrypt RTPCryptor, err error)
}

// MediaSecurityReceiver arms the device side of platform-sent media (talk
// downlink): OpenMediaSecurity consumes the offer's a=security value and
// returns the payload decryptor (level C) and the platform-signature
// verifier (level B/C). Nil hooks = plaintext.
type MediaSecurityReceiver interface {
	OpenMediaSecurity(offerValue string) (decrypt RTPCryptor, verify AUVerifierFunc, err error)
}

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
