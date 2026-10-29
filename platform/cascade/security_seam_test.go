package cascade

import (
	"testing"

	"github.com/ghettovoice/gosip/sip"
	"github.com/stretchr/testify/require"

	"github.com/mickeyzzc/gb28181-go/device"
)

// The GB35114 signaling seams on the cascade path are tag-free; these
// default-build tests pin the unarmed contract: with no upper, or an
// authenticator that neither verifies nor signs, requests flow exactly as
// the legacy Digest cascade did.

// plainAuthenticator implements only the base RegisterAuthenticator —
// neither IncomingNoteVerifier nor OutgoingSigner.
type plainAuthenticator struct{}

func (plainAuthenticator) InitialAuthorization() string                  { return "" }
func (plainAuthenticator) AuthorizeWithChallenge(string) (string, error) { return "", nil }
func (plainAuthenticator) VerifyOK(string) error                         { return nil }
func (plainAuthenticator) DecorateOutgoing(_, _, _, _, _ string) (string, string) {
	return "", ""
}

func TestAllowSecuredRequest_UnarmedPaths(t *testing.T) {
	// No uppers at all: legacy behavior, everything allowed.
	svc := &Service{}
	req := newFromRequest(t, "34020000002000000001")
	require.True(t, svc.allowSecuredRequest(req), "no upper must allow")

	// An upper whose authenticator is not a Note verifier: allowed even
	// with a Note header present (nothing to verify with).
	svc = &Service{uppers: []*upper{{cfg: Upstream{RegisterAuthenticator: plainAuthenticator{}}}}}
	req = newFromRequest(t, "34020000002000000001")
	req.AppendHeader(&sip.GenericHeader{HeaderName: "Note", Contents: `Digest nonce="x",algorithm=SM3`})
	require.True(t, svc.allowSecuredRequest(req), "non-verifier authenticator must allow")

	// An upper with no authenticator at all: Digest cascade, allowed.
	svc = &Service{uppers: []*upper{{cfg: Upstream{}}}}
	require.True(t, svc.allowSecuredRequest(req), "nil authenticator must allow")
}

func TestDecorateSecured_NonSignerIsNoop(t *testing.T) {
	svc := &Service{uppers: []*upper{{cfg: Upstream{RegisterAuthenticator: plainAuthenticator{}}}}}
	req := newFromRequest(t, "34020000002000000001")
	before := len(req.GetHeaders("Date")) + len(req.GetHeaders("Note"))
	svc.decorateSecured(svc.uppers[0], req)
	after := len(req.GetHeaders("Date")) + len(req.GetHeaders("Note"))
	require.Equal(t, before, after, "a non-signer authenticator must not stamp Date/Note")
}

// Compile-time interface checks: the base seam really is the base — the
// gb35114-tagged authenticators add the verifier/signer halves.
var _ device.RegisterAuthenticator = plainAuthenticator{}
