package sip

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/ghettovoice/gosip/sip"
)

// stubRegisterAuthenticator pins the seam contract without any crypto: the
// routing in handleRegister/handleMessage is what's under test here.
type stubRegisterAuthenticator struct {
	mu sync.Mutex

	challengeWWWAuth string
	challengeErr     error
	verifySecurity   string
	verifyErr        error
	noteErr          error

	sawChallengeAuth string
	sawVerifyAuth    string
	sawNote          bool
}

func (s *stubRegisterAuthenticator) Challenge(deviceID, authorization string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sawChallengeAuth = authorization
	return s.challengeWWWAuth, s.challengeErr
}

func (s *stubRegisterAuthenticator) VerifyRegister(deviceID, authorization string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sawVerifyAuth = authorization
	return s.verifySecurity, s.verifyErr
}

func (s *stubRegisterAuthenticator) VerifyNote(deviceID, note, method, from, to, callID, date, body string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sawNote = true
	return s.noteErr
}

func authHeader(value string) *sip.GenericHeader {
	return &sip.GenericHeader{HeaderName: "Authorization", Contents: value}
}

func TestRegisterAuthenticator_Gb35114Flow(t *testing.T) {
	cfg := testConfig(t)
	stub := &stubRegisterAuthenticator{
		challengeWWWAuth: `Bidirection algorithm="A:SM2;H:SM3", random1="cmFuZG9tMQ=="`,
		verifySecurity:   `Bidirection cryptkey="c3R1Yg==", sign2="c2lnbg=="`,
	}
	cfg.RegisterAuthenticator = stub
	_, dm := startTestServer(t, cfg)
	client := newSIPClient(t, cfg.SIPListen)

	capability := `Capability algorithm="A:SM2;H:SM3", keyversion="2026-01-01T00:00:00.000"`
	req := buildRequest(t, sip.REGISTER, testDeviceID, testServerID, cfg.SIPListen, client.localPort(), "", authHeader(capability))
	res := client.roundTrip(req)
	if res.StatusCode() != 401 {
		t.Fatalf("Capability REGISTER status = %d, want 401", res.StatusCode())
	}
	var wwwAuth string
	for _, h := range res.GetHeaders("WWW-Authenticate") {
		if gh, ok := h.(*sip.GenericHeader); ok {
			wwwAuth = gh.Contents
		}
	}
	if wwwAuth != stub.challengeWWWAuth {
		t.Fatalf("401 WWW-Authenticate = %q, want the authenticator's challenge", wwwAuth)
	}
	stub.mu.Lock()
	if stub.sawChallengeAuth != capability {
		t.Fatalf("Challenge saw authorization %q", stub.sawChallengeAuth)
	}
	stub.mu.Unlock()

	verified := `Bidirection random1="cmFuZG9tMQ==", random2="cmFuZG9tMg==", sign1="c2lnbTE="`
	req2 := buildRequest(t, sip.REGISTER, testDeviceID, testServerID, cfg.SIPListen, client.localPort(), "", authHeader(verified))
	res2 := client.roundTrip(req2)
	if res2.StatusCode() != 200 {
		t.Fatalf("verified REGISTER status = %d, want 200", res2.StatusCode())
	}
	var securityInfo string
	for _, h := range res2.GetHeaders("SecurityInfo") {
		if gh, ok := h.(*sip.GenericHeader); ok {
			securityInfo = gh.Contents
		}
	}
	if securityInfo != stub.verifySecurity {
		t.Fatalf("200 OK SecurityInfo = %q, want the authenticator's value", securityInfo)
	}
	stub.mu.Lock()
	if stub.sawVerifyAuth != verified {
		t.Fatalf("VerifyRegister saw authorization %q", stub.sawVerifyAuth)
	}
	stub.mu.Unlock()

	if _, ok := dm.Device(testDeviceID); !ok {
		t.Fatalf("device %s not registered after GB35114 handshake", testDeviceID)
	}
}

func TestRegisterAuthenticator_VerifyFails(t *testing.T) {
	cfg := testConfig(t)
	cfg.RegisterAuthenticator = &stubRegisterAuthenticator{
		challengeWWWAuth: `Bidirection algorithm="A:SM2;H:SM3", random1="cmFuZG9tMQ=="`,
		verifyErr:        errors.New("bad sign1"),
	}
	startTestServer(t, cfg)
	client := newSIPClient(t, cfg.SIPListen)

	req := buildRequest(t, sip.REGISTER, testDeviceID, testServerID, cfg.SIPListen, client.localPort(), "",
		authHeader(`Bidirection random1="cmFuZG9tMQ==", sign1="bad"`))
	res := client.roundTrip(req)
	if res.StatusCode() != 403 {
		t.Fatalf("failing REGISTER status = %d, want 403", res.StatusCode())
	}
}

func TestRegisterAuthenticator_DigestStillWorks(t *testing.T) {
	// A configured A-level authenticator must not disturb the Digest flow.
	cfg := testConfig(t)
	cfg.RegisterAuthenticator = &stubRegisterAuthenticator{
		challengeWWWAuth: `Bidirection algorithm="A:SM2;H:SM3", random1="cmFuZG9tMQ=="`,
		verifySecurity:   `Bidirection cryptkey="c3R1Yg=="`,
	}
	_, dm := startTestServer(t, cfg)
	client := newSIPClient(t, cfg.SIPListen)

	req := buildRequest(t, sip.REGISTER, testDeviceID, testServerID, cfg.SIPListen, client.localPort(), "")
	res := client.roundTrip(req)
	if res.StatusCode() != 401 {
		t.Fatalf("first REGISTER status = %d, want 401 digest challenge", res.StatusCode())
	}
	auth := digestAuth(t, getChallenge(t, res), req, cfg.Password)
	req2 := buildRequest(t, sip.REGISTER, testDeviceID, testServerID, cfg.SIPListen, client.localPort(), "", auth)
	res2 := client.roundTrip(req2)
	if res2.StatusCode() != 200 {
		t.Fatalf("digest REGISTER status = %d, want 200", res2.StatusCode())
	}
	if _, ok := dm.Device(testDeviceID); !ok {
		t.Fatalf("digest device not registered")
	}
}

func TestRegisterAuthenticator_NoteVerification(t *testing.T) {
	cfg := testConfig(t)
	stub := &stubRegisterAuthenticator{noteErr: errors.New("bad note")}
	cfg.RegisterAuthenticator = stub
	startTestServer(t, cfg)
	client := newSIPClient(t, cfg.SIPListen)

	// The keepalive handler requires a registered device; register via
	// Digest first (the A-level stub doesn't disturb it).
	reg := buildRequest(t, sip.REGISTER, testDeviceID, testServerID, cfg.SIPListen, client.localPort(), "")
	regRes := client.roundTrip(reg)
	if regRes.StatusCode() != 401 {
		t.Fatalf("digest challenge status = %d", regRes.StatusCode())
	}
	reg2 := buildRequest(t, sip.REGISTER, testDeviceID, testServerID, cfg.SIPListen, client.localPort(), "",
		digestAuth(t, getChallenge(t, regRes), reg, cfg.Password))
	if res := client.roundTrip(reg2); res.StatusCode() != 200 {
		t.Fatalf("digest register status = %d", res.StatusCode())
	}

	body := `<?xml version="1.0"?><Notify><CmdType>Keepalive</CmdType><SN>1</SN><DeviceID>` + testDeviceID + `</DeviceID><Status>OK</Status></Notify>`
	req := buildRequest(t, sip.MESSAGE, testDeviceID, testServerID, cfg.SIPListen, client.localPort(), body,
		&sip.GenericHeader{HeaderName: "Note", Contents: `Digest nonce="bm90ZQ==",algorithm=SM3`},
		&sip.GenericHeader{HeaderName: "Date", Contents: "2026-09-08T07:00:00.000"})
	res := client.roundTrip(req)
	if res.StatusCode() != 403 {
		t.Fatalf("MESSAGE with bad Note status = %d, want 403", res.StatusCode())
	}
	stub.mu.Lock()
	if !stub.sawNote {
		t.Fatalf("VerifyNote was not consulted")
	}
	stub.mu.Unlock()

	// Without a Note header the request flows through untouched.
	stub.mu.Lock()
	stub.noteErr = nil
	stub.sawNote = false
	stub.mu.Unlock()
	req2 := buildRequest(t, sip.MESSAGE, testDeviceID, testServerID, cfg.SIPListen, client.localPort(), body)
	res2 := client.roundTrip(req2)
	if res2.StatusCode() != 200 {
		t.Fatalf("MESSAGE without Note status = %d, want 200", res2.StatusCode())
	}
	stub.mu.Lock()
	if stub.sawNote {
		t.Fatalf("VerifyNote consulted for a Note-less request")
	}
	stub.mu.Unlock()
}

func TestAuthScheme(t *testing.T) {
	cases := map[string]string{
		`Digest username="u"`:            "Digest",
		`Capability algorithm="A:SM2"`:   "Capability",
		`Bidirection random1="x", sign1`: "Bidirection",
		"Unidirection\trandom1=\"x\"":    "Unidirection",
		"":                               "",
	}
	for in, want := range cases {
		if got := authScheme(in); got != want {
			t.Fatalf("authScheme(%q) = %q, want %q", in, got, want)
		}
	}
}

// registerNTimes performs n digest REGISTER attempts (fresh challenge each
// time) with the given password, returning the statuses seen.
func registerNTimes(t *testing.T, cfg Config, client *sipClient, password string, n int) []int {
	t.Helper()
	statuses := make([]int, 0, n)
	for i := 0; i < n; i++ {
		req := buildRequest(t, sip.REGISTER, testDeviceID, testServerID, cfg.SIPListen, client.localPort(), "")
		res := client.roundTrip(req)
		if res.StatusCode() == 401 {
			auth := digestAuth(t, getChallenge(t, res), req, password)
			req2 := buildRequest(t, sip.REGISTER, testDeviceID, testServerID, cfg.SIPListen, client.localPort(), "", auth)
			res2 := client.roundTrip(req2)
			statuses = append(statuses, int(res2.StatusCode()))
			continue
		}
		statuses = append(statuses, int(res.StatusCode()))
	}
	return statuses
}

func TestRegisterAuthLockout(t *testing.T) {
	cfg := testConfig(t)
	cfg.RegisterFailureLimit = 3
	cfg.RegisterLockoutDuration = "500ms"
	startTestServer(t, cfg)
	client := newSIPClient(t, cfg.SIPListen)

	// Three failed digests exhaust the budget…
	got := registerNTimes(t, cfg, client, "wrong", 3)
	for _, s := range got {
		if s != 403 {
			t.Fatalf("failed digests before lockout: statuses %v, want all 403", got)
		}
	}
	// …the fourth REGISTER is locked out before any digest check.
	req := buildRequest(t, sip.REGISTER, testDeviceID, testServerID, cfg.SIPListen, client.localPort(), "")
	res := client.roundTrip(req)
	if res.StatusCode() != 403 {
		t.Fatalf("locked-out REGISTER status = %d, want 403", res.StatusCode())
	}

	// The lockout expires and a correct password works again.
	time.Sleep(600 * time.Millisecond)
	statuses := registerNTimes(t, cfg, client, cfg.Password, 1)
	if len(statuses) != 1 || statuses[0] != 200 {
		t.Fatalf("post-lockout register statuses = %v, want [200]", statuses)
	}
}

func TestRegisterAuthLockoutDisabled(t *testing.T) {
	cfg := testConfig(t)
	cfg.RegisterFailureLimit = -1 // explicit opt-out keeps the legacy behavior
	startTestServer(t, cfg)
	client := newSIPClient(t, cfg.SIPListen)
	got := registerNTimes(t, cfg, client, "wrong", 7)
	for _, s := range got {
		if s != 403 {
			t.Fatalf("statuses = %v, want all 403 (no lockout)", got)
		}
	}
	// Still not locked out: the correct password registers.
	statuses := registerNTimes(t, cfg, client, cfg.Password, 1)
	if len(statuses) != 1 || statuses[0] != 200 {
		t.Fatalf("statuses = %v, want [200]", statuses)
	}
}

func TestRegisterStrictAuth(t *testing.T) {
	cfg := testConfig(t)
	cfg.Password = ""
	cfg.StrictAuth = true // no password + no A-level authenticator → refuse
	_, dm := startTestServer(t, cfg)
	client := newSIPClient(t, cfg.SIPListen)

	req := buildRequest(t, sip.REGISTER, testDeviceID, testServerID, cfg.SIPListen, client.localPort(), "")
	res := client.roundTrip(req)
	if res.StatusCode() != 403 {
		t.Fatalf("strict-auth REGISTER status = %d, want 403", res.StatusCode())
	}
	if _, ok := dm.Device(testDeviceID); ok {
		t.Fatalf("device must not register under strict auth")
	}
}
