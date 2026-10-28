package sip

// RegisterAuthenticator authenticates REGISTERs that carry a non-Digest
// Authorization scheme — GB 35114 A-level today. The concrete
// implementation lives behind the gb35114 build tag (security35114.Platform)
// and is injected through Config.RegisterAuthenticator; the structural
// method set keeps this file free of that dependency.
type RegisterAuthenticator interface {
	// Challenge issues the WWW-Authenticate header value for a first
	// REGISTER — e.g. the GB35114 401 carrying random1. authorization is
	// the raw Authorization header of that REGISTER ("" when absent, e.g.
	// the Capability announcement of a GB35114 device).
	Challenge(deviceID, authorization string) (string, error)
	// VerifyRegister validates the Authorization of the retried REGISTER
	// and returns the SecurityInfo header value carried by the 200 OK.
	VerifyRegister(deviceID, authorization string) (string, error)
	// VerifyNote validates the Note header of an incoming non-REGISTER
	// request ("" = absent — implementations treat it as pass-through).
	VerifyNote(deviceID, note, method, from, to, callID, date, body string) error
}

// DownstreamSigner stamps integrity headers onto platform→device requests
// (the GB35114 Date + keyed-SM3 Note pair; the standard's §9.4 digest is
// bidirectional). Implementations return empty strings to leave a request
// untouched (no security session for the device).
type DownstreamSigner interface {
	DecorateDownstream(deviceID, method, from, to, callID, body string) (date, note string)
}

// MediaSecurityNegotiator negotiates GB35114 B/C media security on INVITE
// dialogs. MediaSDPOffer feeds the offer's a=security attribute ("" = no
// media security); ArmMediaSecurity inspects the device's answer SDP and
// returns the receiver hooks — the RTP payload decryptor (level C) and the
// per-AU signature verifier (level B). Both nil = plaintext session.
type MediaSecurityNegotiator interface {
	MediaSDPOffer() string
	ArmMediaSecurity(deviceID string, answerSDP []byte) (
		decrypt func(payload []byte, ssrc uint32, seq uint16) []byte,
		verifyAU func(au []byte, ssrc, timestamp uint32, sig []byte) error,
		err error)
}
