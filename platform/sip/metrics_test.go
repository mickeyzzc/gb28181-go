package sip

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ghettovoice/gosip/sip"
	"github.com/mickeyzzc/gb28181-go/metrics"
	"github.com/mickeyzzc/gb28181-go/platform"
)

// countingSipHooks records hook firings for assertions (issue #40).
type countingSipHooks struct {
	ok, fail, started, stopped atomic.Int32
}

func (c *countingSipHooks) RegisterAttempt()      {}
func (c *countingSipHooks) RegisterOK()           { c.ok.Add(1) }
func (c *countingSipHooks) RegisterFail()         { c.fail.Add(1) }
func (c *countingSipHooks) KeepaliveFail()        {}
func (c *countingSipHooks) InviteSessionStarted() { c.started.Add(1) }
func (c *countingSipHooks) InviteSessionStopped() { c.stopped.Add(1) }
func (c *countingSipHooks) InviteFail()           {}
func (c *countingSipHooks) PSBytesOut(int64)      {}

var _ metrics.Hooks = (*countingSipHooks)(nil)

// startMetricsServer is startTestServer with hooks installed.
func startMetricsServer(t *testing.T, cfg Config, hooks metrics.Hooks) *Server {
	t.Helper()
	base := int(20000 + 100*testPortBase.Add(1))
	dm := platform.NewDeviceManager(60 * time.Second)
	srv := NewServer(cfg, dm, platform.NewSessionManager(platform.NewPortManager(uint16(base), uint16(base+99)), cfg.ServerID), nil)
	srv.SetMetricsHooks(hooks)
	if err := srv.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = srv.Stop() })
	return srv
}

// Registration outcomes drive the hooks over the real wire: digest success
// → RegisterOK; bad credentials → RegisterFail.
func TestRegisterMetricsHooks(t *testing.T) {
	cfg := testConfig(t)
	hooks := &countingSipHooks{}
	startMetricsServer(t, cfg, hooks)
	client := newSIPClient(t, cfg.SIPListen)

	req := buildRequest(t, sip.REGISTER, testDeviceID, testServerID, cfg.SIPListen, client.localPort(), "")
	res := client.roundTrip(req)
	challenge := getChallenge(t, res)

	// Bad digest first → 403 → RegisterFail.
	bad := digestAuth(t, challenge, req, "wrong-password")
	reqBad := buildRequest(t, sip.REGISTER, testDeviceID, testServerID, cfg.SIPListen, client.localPort(), "", bad)
	if res2 := client.roundTrip(reqBad); res2.StatusCode() != 403 {
		t.Fatalf("bad-digest REGISTER status = %d, want 403", res2.StatusCode())
	}
	if got := hooks.fail.Load(); got != 1 {
		t.Errorf("RegisterFail = %d, want 1", got)
	}

	// Good digest → 200 → RegisterOK.
	auth := digestAuth(t, challenge, req, cfg.Password)
	req2 := buildRequest(t, sip.REGISTER, testDeviceID, testServerID, cfg.SIPListen, client.localPort(), "", auth)
	if res2 := client.roundTrip(req2); res2.StatusCode() != 200 {
		t.Fatalf("authed REGISTER status = %d, want 200", res2.StatusCode())
	}
	if got := hooks.ok.Load(); got != 1 {
		t.Errorf("RegisterOK = %d, want 1", got)
	}
}

// First media on a session fires InviteSessionStarted; a BYE without a
// dialog must not fire InviteSessionStopped (nil-safe path).
func TestInviteSessionHooks(t *testing.T) {
	cfg := testConfig(t)
	hooks := &countingSipHooks{}
	srv := startMetricsServer(t, cfg, hooks)

	srv.onFirstRTP(testDeviceID)
	if got := hooks.started.Load(); got != 1 {
		t.Errorf("InviteSessionStarted = %d, want 1", got)
	}

	if err := srv.sendByeForChannel(testDeviceID); err != nil {
		t.Fatalf("sendByeForChannel without dialog: %v", err)
	}
	if got := hooks.stopped.Load(); got != 0 {
		t.Errorf("InviteSessionStopped without dialog = %d, want 0", got)
	}
}
