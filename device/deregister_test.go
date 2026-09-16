package device_test

// Wire-level tests for the graceful de-registration helper (issue #84):
// a real device Server over real UDP sockets, platform side driven by
// hand — the Go twin of gb28181-rs #73's lifecycle suite.

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/mickeyzzc/gb28181-go/device"
)

// readRegisterUntil drains the platform socket until the next REGISTER
// (skipping keepalive MESSAGEs and other noise).
func readRegisterUntil(t *testing.T, platConn *net.UDPConn) device.SipMessage {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		msg, _ := readSnapMsg(t, platConn)
		if msg.Method == "REGISTER" {
			return msg
		}
	}
	t.Fatalf("no REGISTER within 5s")
	return device.SipMessage{}
}

func replySnap(t *testing.T, platConn *net.UDPConn, devAddr *net.UDPAddr, req device.SipMessage, status int, wwwAuth string) {
	t.Helper()
	resp := device.SipMessage{
		StatusCode: status,
		Via:        req.Via,
		From:       req.From,
		To:         req.To,
		CallID:     req.CallID,
		CSeq:       req.CSeq,
		Headers:    map[string]string{},
	}
	if wwwAuth != "" {
		resp.WWWAuthenticate = wwwAuth
	}
	writeSnapMsg(t, platConn, resp, devAddr)
}

// Deregister must send REGISTER with Expires: 0 and complete the 401
// Digest dance on the wire.
func TestDeregisterSendsExpiresZero(t *testing.T) {
	platConn, devAddr, srv := startWireTestServerFull(t, func(*device.Server) {}, func(c *device.Config) {
		c.RegisterAuthenticator = nil // classic Digest path: leg 2 must carry a real Authorization
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- srv.Deregister(ctx) }()

	// Leg 1: unauthenticated, Expires: 0.
	reg := readRegisterUntil(t, platConn)
	if reg.Expires != "0" {
		t.Fatalf("de-register leg 1 Expires = %q, want 0 (%+v)", reg.Expires, reg)
	}
	if reg.Authorization != "" {
		t.Fatalf("de-register leg 1 must be unauthenticated: %+v", reg)
	}
	replySnap(t, platConn, devAddr, reg, 401, `Digest realm="3402000000", nonce="dz", algorithm=MD5`)

	// Leg 2: authenticated, Expires: 0, same dialog.
	reg2 := readRegisterUntil(t, platConn)
	if reg2.Expires != "0" {
		t.Fatalf("de-register leg 2 Expires = %q, want 0 (%+v)", reg2.Expires, reg2)
	}
	if reg2.Authorization == "" {
		t.Fatalf("de-register leg 2 must answer the 401: %+v", reg2)
	}
	if reg2.CallID != reg.CallID {
		t.Fatalf("de-register legs must share Call-ID: %q vs %q", reg.CallID, reg2.CallID)
	}
	replySnap(t, platConn, devAddr, reg2, 200, "")

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Deregister: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Deregister did not return after the 200 OK")
	}
	srv.Stop()
}

// A platform accepting the unauthenticated de-register outright (200 on
// leg 1) ends the dance in one round-trip.
func TestDeregisterAcceptsUnauth200(t *testing.T) {
	platConn, devAddr, srv := startWireTestServerFull(t, func(*device.Server) {}, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- srv.Deregister(ctx) }()

	reg := readRegisterUntil(t, platConn)
	if reg.Expires != "0" {
		t.Fatalf("de-register leg 1 Expires = %q, want 0", reg.Expires)
	}
	replySnap(t, platConn, devAddr, reg, 200, "")

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Deregister: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Deregister did not return after the direct 200")
	}
	srv.Stop()
}

// A silent platform must not block the host: the 2s response timeout
// errors out and Stop() still runs.
func TestDeregisterToleratesSilentPlatform(t *testing.T) {
	_, _, srv := startWireTestServerFull(t, func(*device.Server) {}, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	started := time.Now()
	if err := srv.Deregister(ctx); err == nil {
		t.Fatal("a silent platform must surface an error")
	}
	elapsed := time.Since(started)
	if elapsed < 1500*time.Millisecond || elapsed > 4*time.Second {
		t.Fatalf("one 2s timeout then return, took %v", elapsed)
	}
	done := make(chan struct{})
	go func() { srv.Stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Stop() blocked after a failed Deregister")
	}
}
