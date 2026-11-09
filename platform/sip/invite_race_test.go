package sip

// Regression test for issue #169: concurrent InviteChannel calls used to race
// the receiver idempotency check — both callers saw no receiver, both ran
// inviteCore, and the second sessionMgr.Invite recycled the first's half-open
// session mid-handshake (Invite's idempotency guard BYEs whatever it finds),
// tearing each other's media down. The per-channel invite gate makes the
// check-then-invite sequence atomic; concurrent callers serialize and the
// losers observe the winner's receiver.

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ghettovoice/gosip/sip"
	"github.com/mickeyzzc/gb28181-go/platform"
	"github.com/stretchr/testify/require"
)

func TestInviteChannelConcurrentCallsSerialize(t *testing.T) {
	cfg := testConfig(t)
	srv, dm := startTestServer(t, cfg)
	client := newSIPClient(t, cfg.SIPListen)

	// No enroller: InviteChannel runs without a bound recorder (cameraID ""
	// skips the deferral gate), so the SIP handshake is the only moving part.
	dm.Register(&platform.Device{ID: testDeviceID, NetAddr: client.conn.LocalAddr().String()})
	dm.RegisterChannel(testDeviceID, &platform.Channel{ID: fakeChannelID})

	// Responder: answer the INVITE (and its retransmissions) with a delayed
	// 200 so concurrent callers overlap in the pre-fix race window. Distinct
	// INVITE Call-IDs and any mid-flight BYE are counted — pre-fix, the
	// second caller's sessionMgr.Invite emitted a dialog-reset BYE against
	// the first's half-open session.
	var callIDs sync.Map
	var invites atomic.Int64
	var byes atomic.Int64
	stopResponder := make(chan struct{})
	go func() {
		for {
			select {
			case <-stopResponder:
				return
			default:
			}
			req := client.nextRequest(50 * time.Millisecond)
			if req == nil {
				continue
			}
			switch req.Method() {
			case sip.INVITE:
				callID, _ := req.CallID()
				if _, dup := callIDs.LoadOrStore(callID.String(), true); !dup {
					invites.Add(1)
				}
				// Hold the answer so a second un-gated INVITE would land in
				// the same window (the pre-fix interleaving).
				time.Sleep(250 * time.Millisecond)
				client.respondRaw(req, 200, "OK", string(req.Body()), "application/sdp")
			case sip.BYE:
				byes.Add(1)
				client.respondRaw(req, 200, "OK", "", "")
			}
		}
	}()

	const callers = 3
	results := make(chan error, callers)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results <- srv.InviteChannel(testDeviceID, fakeChannelID)
		}()
	}
	close(start)
	wg.Wait()
	close(stopResponder)
	close(results)
	for err := range results {
		require.NoError(t, err, "every concurrent InviteChannel must succeed")
	}

	// Exactly one INVITE transaction for the channel, and no mid-flight BYE
	// — the second/third callers found the winner's receiver and no-op'd.
	require.Equal(t, int64(1), invites.Load(), "concurrent InviteChannel must collapse into one INVITE")
	require.Zero(t, byes.Load(), "no caller may recycle another's session mid-handshake")

	srv.mu.Lock()
	dlg := srv.dialogs[fakeChannelID]
	srv.mu.Unlock()
	require.NotNil(t, dlg, "the winning INVITE must complete its dialog")

	_ = srv.Stop()
}
