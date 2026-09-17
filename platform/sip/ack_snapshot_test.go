package sip

import (
	"sync"
	"testing"
	"time"

	"github.com/ghettovoice/gosip/sip"
	"github.com/stretchr/testify/require"
)

// #95: gosip's transport layer rewrites the top Via of an outgoing request in
// place on every send — retransmissions included, unsynchronized
// (transport/layer.go Send). These tests pin the ACK-construction contract
// against that: ACKs must be buildable from a snapshot taken before the
// INVITE was handed over, with zero reads of the (concurrently mutated)
// original, and the resulting wire format must match a directly-built ACK.

// rewriteViaLikeGosipTransport flips the Via sent-by fields the way gosip's
// transport layer does on every (re)send of a request.
func rewriteViaLikeGosipTransport(req sip.Request) {
	viaHop, ok := req.ViaHop()
	if !ok {
		return
	}
	viaHop.Transport = "UDP"
	viaHop.Host = "198.51.100.7"
	port := sip.Port(5060)
	viaHop.Port = &port
	req.SetViaHop(viaHop)
}

func TestInviteSnapshotAckIsolationUnderViaRewrite(t *testing.T) {
	invite := buildRequest(t, sip.INVITE, testDeviceID, testServerID, "127.0.0.1:5060", 40000, "v=0\r\n")
	resp := sip.NewResponseFromRequest("", invite, 200, "OK", "v=0\r\n")

	// Snapshot strictly before the INVITE would be handed to the tx layer.
	snap := newInviteSnapshot(invite)

	// Simulate gosip's retransmit-time Via rewrites hitting the original
	// while the invite flow builds its ACKs from the snapshot.
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				rewriteViaLikeGosipTransport(invite)
			}
		}
	}()

	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		ack := snap.ackFor(resp)
		require.Equal(t, sip.ACK, ack.Method())
		spec := snap.speculativeAck()
		require.Equal(t, sip.ACK, spec.Method())
	}
	close(stop)
	wg.Wait()
}

func TestInviteSnapshotAckWireEquivalent(t *testing.T) {
	invite := buildRequest(t, sip.INVITE, testDeviceID, testServerID, "127.0.0.1:5060", 40000, "v=0\r\n")
	resp := sip.NewResponseFromRequest("", invite, 200, "OK", "v=0\r\n")

	direct := sip.NewAckRequest("", invite, resp, "", nil)
	snap := newInviteSnapshot(invite)
	fromSnap := snap.ackFor(resp)

	// Dialog identifiers and routing must be identical.
	invCID, ok := invite.CallID()
	require.True(t, ok)
	directCID, ok := direct.CallID()
	require.True(t, ok)
	snapCID, ok := fromSnap.CallID()
	require.True(t, ok)
	require.Equal(t, invCID.Value(), snapCID.Value(), "snapshot ACK Call-ID must match the INVITE")
	require.Equal(t, directCID.Value(), snapCID.Value())

	require.Equal(t, direct.Recipient().String(), fromSnap.Recipient().String())
	require.Equal(t, direct.Source(), fromSnap.Source())
	require.Equal(t, direct.Destination(), fromSnap.Destination())

	directCSeq, ok := direct.CSeq()
	require.True(t, ok)
	snapCSeq, ok := fromSnap.CSeq()
	require.True(t, ok)
	require.Equal(t, directCSeq.SeqNo, snapCSeq.SeqNo)
	require.Equal(t, sip.ACK, snapCSeq.MethodName)

	// Via sent-by matches; the branch is regenerated per 2xx-ACK by design.
	directVia, ok := direct.ViaHop()
	require.True(t, ok)
	snapVia, ok := fromSnap.ViaHop()
	require.True(t, ok)
	require.Equal(t, directVia.Host, snapVia.Host)
	require.Equal(t, directVia.Transport, snapVia.Transport)
	directBranch, ok := directVia.Params.Get("branch")
	require.True(t, ok)
	snapBranch, ok := snapVia.Params.Get("branch")
	require.True(t, ok)
	require.NotEqual(t, directBranch.String(), snapBranch.String(),
		"each 2xx ACK regenerates its branch — not an equality contract")
}
