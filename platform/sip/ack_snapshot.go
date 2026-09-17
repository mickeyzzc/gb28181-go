package sip

import (
	"github.com/ghettovoice/gosip/sip"
)

// inviteSnapshot is a pre-send deep copy of an INVITE request, taken before
// the request is handed to gosip's transaction layer (#95).
//
// gosip's transport layer rewrites the top Via of an outgoing request in
// place on every send — retransmissions included — without synchronizing the
// header fields (transport/layer.go Send: viaHop.Transport/Host/Port).
// ACK construction reads those same fields (sip.NewAckRequest →
// CopyHeaders("Via") → header.Clone), so a 2xx landing while the INVITE's
// last retransmit is mid-rewrite is a data race: the reader runs in the
// invite flow, the writer on the transaction goroutine. Building every ACK
// for the dialog from this snapshot removes the shared memory entirely. It
// is wire-equivalent: NewAckRequest regenerates the Via branch for 2xx ACKs,
// and the transport layer rewrites the sent-by (host/port/transport) on the
// ACK's own send anyway.
type inviteSnapshot struct {
	req sip.Request
}

// newInviteSnapshot must be called before srv.Request(invite): afterwards
// gosip owns the original and may mutate its Via concurrently.
func newInviteSnapshot(invite sip.Request) inviteSnapshot {
	if clone, ok := invite.Clone().(sip.Request); ok {
		return inviteSnapshot{req: clone}
	}
	return inviteSnapshot{req: invite}
}

// ackFor builds the in-dialog ACK for the device's 2xx answer.
func (s inviteSnapshot) ackFor(resp sip.Response) sip.Request {
	return sip.NewAckRequest("", s.req, resp, "", nil)
}

// speculativeAck builds the no-answer fallback ACK (awaitInviteAnswer).
func (s inviteSnapshot) speculativeAck() sip.Request {
	return buildSpeculativeAck(s.req)
}
