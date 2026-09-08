package device

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mickeyzzc/gb28181-go/metrics"
)

// countingHooks records every hook firing for assertions (issue #40).
type countingHooks struct {
	attempt, ok, fail, keepaliveFail, psBytes atomic.Int32
}

func (c *countingHooks) RegisterAttempt()      { c.attempt.Add(1) }
func (c *countingHooks) RegisterOK()           { c.ok.Add(1) }
func (c *countingHooks) RegisterFail()         { c.fail.Add(1) }
func (c *countingHooks) KeepaliveFail()        { c.keepaliveFail.Add(1) }
func (c *countingHooks) InviteSessionStarted() {}
func (c *countingHooks) InviteSessionStopped() {}
func (c *countingHooks) InviteFail()           {}
func (c *countingHooks) PSBytesOut(n int64)    { c.psBytes.Add(int32(n)) }

var _ metrics.Hooks = (*countingHooks)(nil)

// A successful REGISTER lifecycle drives RegisterAttempt + RegisterOK; a
// rejected one drives RegisterAttempt + RegisterFail.
func TestRegisterMetricsHooks(t *testing.T) {
	run := func(reply func(reg1 SipMessage, peer *net.UDPAddr, conn *net.UDPConn)) *countingHooks {
		t.Helper()
		platConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
		if err != nil {
			t.Fatalf("listen: %v", err)
		}
		defer platConn.Close()

		srv := newSeamTestServer(t, nil, platConn)
		hooks := &countingHooks{}
		srv.SetMetricsHooks(hooks)

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		errCh := make(chan error, 1)
		go func() { errCh <- srv.runRegisterLifecycleWith(ctx, srv.socketResponseSource()) }()

		reg1, peer := readSIP(t, platConn)
		reply(reg1, peer, platConn)
		<-errCh
		return hooks
	}

	okHooks := run(func(reg1 SipMessage, peer *net.UDPAddr, conn *net.UDPConn) {
		writeSIP(t, conn, SipMessage{
			StatusCode: 200,
			Via:        reg1.Via,
			From:       reg1.From,
			To:         reg1.To + ";tag=fake",
			CallID:     reg1.CallID,
			CSeq:       reg1.CSeq,
			Headers:    map[string]string{},
		}, peer)
	})
	if got := okHooks.attempt.Load(); got != 1 {
		t.Errorf("RegisterAttempt = %d, want 1", got)
	}
	if got := okHooks.ok.Load(); got != 1 {
		t.Errorf("RegisterOK = %d, want 1", got)
	}
	if got := okHooks.fail.Load(); got != 0 {
		t.Errorf("RegisterFail = %d, want 0", got)
	}

	failHooks := run(func(reg1 SipMessage, peer *net.UDPAddr, conn *net.UDPConn) {
		writeSIP(t, conn, SipMessage{
			StatusCode: 403,
			Via:        reg1.Via,
			From:       reg1.From,
			To:         reg1.To + ";tag=fake",
			CallID:     reg1.CallID,
			CSeq:       reg1.CSeq,
			Headers:    map[string]string{},
		}, peer)
	})
	if got := failHooks.fail.Load(); got != 1 {
		t.Errorf("RegisterFail = %d, want 1", got)
	}
	if got := failHooks.ok.Load(); got != 0 {
		t.Errorf("RegisterOK on rejection = %d, want 0", got)
	}
}

// The media pusher reports every PS byte handed to the wire.
func TestRtpPusherPSBytesOut(t *testing.T) {
	dst, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer dst.Close()
	src, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer src.Close()

	pusher := NewRtpPusher(src, dst.LocalAddr().(*net.UDPAddr))
	hooks := &countingHooks{}
	pusher.SetMetricsHooks(hooks)

	// One small frame (< MTU → single RTP packet) is enough to pin the
	// byte accounting.
	payload := make([]byte, 512)
	if err := pusher.SendFrame(payload, true, time.Now(), 1); err != nil {
		t.Fatalf("SendFrame: %v", err)
	}
	if got := hooks.psBytes.Load(); got != int32(len(payload)) {
		t.Errorf("PSBytesOut = %d, want %d", got, len(payload))
	}
}
