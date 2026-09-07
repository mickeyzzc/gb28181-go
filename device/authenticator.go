// Opt-in authentication seams for the REGISTER lifecycle. The built-in
// flow is SIP Digest (RFC 2617); setting Config.RegisterAuthenticator
// replaces it — the reference implementation is the build-tagged
// security35114 package (GB 35114 A-level).

package device

// RegisterAuthenticator replaces the built-in Digest REGISTER
// authentication. All methods run inside the register lifecycle; errors
// abort it. The nil zero value keeps the Digest flow.
type RegisterAuthenticator interface {
	// InitialAuthorization returns the Authorization header of the first
	// REGISTER ("" for none), e.g. a GB35114 Capability announcement.
	InitialAuthorization() string

	// AuthorizeWithChallenge consumes the WWW-Authenticate header of the
	// 401 response and returns the Authorization header of the retried
	// REGISTER.
	AuthorizeWithChallenge(wwwAuthenticate string) (string, error)

	// VerifyOK validates the 200 OK that answers the authenticated
	// REGISTER. The securityInfo argument is the 200 OK's SecurityInfo
	// extension header, "" when absent (plain Digest platforms never send
	// one; implementations decide whether that is acceptable — GB 35114
	// fails closed).
	VerifyOK(securityInfo string) error
}

// OutgoingSigner is optionally implemented by a RegisterAuthenticator to
// authenticate every subsequent outgoing SIP request. Implementations
// return the values of the Date and Note headers (GB 35114 §9.4); empty
// strings leave the message untouched.
type OutgoingSigner interface {
	DecorateOutgoing(method, from, to, callID, body string) (date string, note string)
}
