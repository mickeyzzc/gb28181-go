package cascade

import (
	"context"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ghettovoice/gosip/sip"

	"github.com/stretchr/testify/require"

	"github.com/mickeyzzc/gb28181-go/platform"
)

// The GB35114 cascade REGISTER seam is tag-free (the tagged security35114
// package injects the real authenticator in production); this default-build
// loopback pins the wiring: an upper configured with a RegisterAuthenticator
// runs the Capability→401→authorized→200 dance INSTEAD of Digest, with the
// authenticator's headers on both REGISTERs.
func TestLoopbackRegisterGB35114Dance(t *testing.T) {
	hub := platform.NewFrameHub()
	db := newCascadeTestDB(t)
	cfg := testCfg()
	cfg.SIPListen = net.JoinHostPort(lbLocalHost, strconv.Itoa(freeUDPPort(t)))
	up := newUpperSocket(t, cfg.SIPListen)

	auth := &recordingAuthenticator{authorization: "Capability A:SM2;H:SM3"}
	cfg.ServerAddr = "" // force the upstreams[] form — the legacy form carries no authenticator
	cfg.Upstreams = []Upstream{{
		ServerDomain:          cfg.ServerDomain,
		ServerAddr:            up.conn.LocalAddr().String(),
		LocalDeviceID:         cfg.LocalDeviceID,
		Realm:                 cfg.Realm,
		RegisterAuthenticator: auth,
	}}

	svc := New(cfg, hubSource{fakeSource{cams: []CameraInfo{{ID: "cam-1", Name: "Front"}}}, hub}, db)
	require.NoError(t, svc.Start(context.Background()))
	t.Cleanup(func() { _ = svc.Stop() })

	stop := make(chan struct{})
	serve35114Registration(t, up, stop)
	defer close(stop)

	require.Eventually(t, func() bool { return svc.Online() }, 10*time.Second, 100*time.Millisecond,
		"the GB35114 REGISTER dance must complete and register the service")
	require.Equal(t, 1, auth.challenges, "exactly one 401 challenge must be consumed")
	// VerifyOK is only consulted when the 200 carries SecurityInfo; this
	// registrar sends none, so the dance completes without it (the tagged
	// loopbacks cover the SecurityInfo-bearing variant under real SM2).
}

// serve35114Registration plays the upper's GB35114 registrar: a Capability
// announcement draws the 401 challenge (carrying random1); the signed
// re-REGISTER gets 200 (no SecurityInfo — the fake authenticator accepts
// that). Keepalive MESSAGEs are accepted.
func serve35114Registration(t *testing.T, up *upperSocket, stop <-chan struct{}) {
	t.Helper()
	go func() {
		buf := make([]byte, 65535)
		for {
			select {
			case <-stop:
				return
			default:
			}
			_ = up.conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
			n, src, err := up.conn.ReadFromUDP(buf)
			if err != nil {
				continue
			}
			msg, err := parseSIPBytes(buf[:n])
			if err != nil {
				continue
			}
			req, ok := msg.(sip.Request)
			if !ok {
				continue
			}
			switch req.Method() {
			case sip.REGISTER:
				authz := ""
				for _, h := range req.GetHeaders("Authorization") {
					authz = h.Value()
				}
				if strings.Contains(authz, "Capability") {
					chal := `WWW-Authenticate: Unidirection realm="34020000002000000001", random1="cmFuZG9tMQ=="`
					_, _ = up.conn.WriteToUDP(challengeResponse(t, req, 401, "Unauthorized", chal), src)
				} else {
					_, _ = up.conn.WriteToUDP(challengeResponse(t, req, 200, "OK", ""), src)
				}
			case sip.MESSAGE:
				_, _ = up.conn.WriteToUDP(challengeResponse(t, req, 200, "OK", ""), src)
			}
		}
	}()
}

// recordingAuthenticator records the dance progress; its header values are
// visible on the wire so the registrar's Capability-vs-signed dispatch
// drives the 401→200 sequence naturally.
type recordingAuthenticator struct {
	authorization string
	challenges    int
	authorized    int
}

func (a *recordingAuthenticator) InitialAuthorization() string { return a.authorization }
func (a *recordingAuthenticator) AuthorizeWithChallenge(wwwAuthenticate string) (string, error) {
	a.challenges++
	a.authorization = "Unidirection sign1=fake"
	return a.authorization, nil
}

func (a *recordingAuthenticator) VerifyOK(securityInfo string) error {
	a.authorized++
	return nil
}

func (a *recordingAuthenticator) DecorateOutgoing(_, _, _, _, _ string) (string, string) {
	return "", ""
}
