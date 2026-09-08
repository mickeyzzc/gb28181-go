// Package metrics defines the library-neutral observability seam
// (issue #40): the host bridges events to Prometheus (or any backend)
// without this module taking a metrics dependency. Implement only the
// hooks you need; every method has a no-op default via [NoopHooks].
//
// Hooks must be cheap (they fire on hot paths such as every media
// packet); never block inside them.
package metrics

// Hooks are observation points fired by the device (UAC) and platform
// (UAS) roles. The zero-value [NoopHooks] does nothing.
type Hooks interface {
	// RegisterAttempt fires when a device starts a REGISTER lifecycle
	// (initial registration and every refresh/retry).
	RegisterAttempt()
	// RegisterOK fires when the platform accepted the REGISTER.
	RegisterOK()
	// RegisterFail fires when a REGISTER lifecycle ended in error.
	RegisterFail()
	// KeepaliveFail fires when a keepalive MESSAGE failed (send error or
	// rejection).
	KeepaliveFail()
	// InviteSessionStarted fires when an INVITE media session confirmed
	// end-to-end (first media flowing).
	InviteSessionStarted()
	// InviteSessionStopped fires when an INVITE media session stopped
	// (BYE, teardown, or shutdown).
	InviteSessionStopped()
	// InviteFail fires when an INVITE could not be established.
	InviteFail()
	// PSBytesOut fires with the MPEG-PS bytes handed to the wire
	// (device: RTP/PS out; platform: media forwarded).
	PSBytesOut(n int64)
}

// NoopHooks is the default Hooks: every method is a no-op.
type NoopHooks struct{}

func (NoopHooks) RegisterAttempt()      {}
func (NoopHooks) RegisterOK()           {}
func (NoopHooks) RegisterFail()         {}
func (NoopHooks) KeepaliveFail()        {}
func (NoopHooks) InviteSessionStarted() {}
func (NoopHooks) InviteSessionStopped() {}
func (NoopHooks) InviteFail()           {}
func (NoopHooks) PSBytesOut(int64)      {}
