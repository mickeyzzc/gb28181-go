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
