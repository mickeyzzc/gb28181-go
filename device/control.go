package device

// GB/T 28181-2016 §9.3.2 / 2022 §9.3 DeviceControl sub-commands (issue
// #81): the device decodes the shared Control body and fires the matching
// optional host callback; the synchronous 200 OK is the whole answer. A
// sub-command without its callback — or an unrecognized value — keeps the
// explicit control reject, mirroring gb28181-rs #58 (values are identical
// across the twins; cross-crate goldens pin them).

import (
	"github.com/mickeyzzc/gb28181-go/manscdp"
)

// ControlCallbacks hosts the §9.3.2 DeviceControl sub-commands. Every
// callback is optional — a sub-command without its callback is answered
// with the control reject (a fast failure signal for the platform).
// Callbacks fire on the SIP receive goroutine — keep them cheap (copy and
// forward into a channel).
type ControlCallbacks struct {
	// OnForceIFrame handles `<IFrameCmd>Send</IFrameCmd>` — make the next
	// encoded frame an IDR. Platforms send this when starting a pull or
	// after packet loss.
	OnForceIFrame func()
	// OnRecordCmd handles `<RecordCmd>Record|StopRecord</RecordCmd>` —
	// toggle platform-requested local recording.
	OnRecordCmd func(start bool)
	// OnGuardCmd handles `<GuardCmd>SetGuard|ResetGuard</GuardCmd>` —
	// arm/disarm.
	OnGuardCmd func(arm bool)
	// OnResetAlarm handles `<AlarmCmd>ResetAlarm</AlarmCmd>`.
	OnResetAlarm func()
	// OnTeleBoot handles `<TeleBoot>Boot</TeleBoot>` — remote restart.
	// Gate the actual reboot behind an explicit host opt-in; leaving the
	// callback nil makes the command a control reject.
	OnTeleBoot func()
	// OnPTZCmd receives the raw §A.3/A505 command hex; bit-level decode
	// stays with the host (shared decoder planned under gb28181-rs#57).
	OnPTZCmd func(a505Hex string)
}

// callbackFor maps a decoded DeviceControl to the installed callback.
// Nil means "not handled" — the caller keeps its reject behavior.
func (c *ControlCallbacks) callbackFor(dc *manscdp.DeviceControl) func() {
	switch {
	case dc.IFrameCmd == "Send":
		return c.OnForceIFrame
	case dc.RecordCmd == "Record":
		if c.OnRecordCmd != nil {
			return func() { c.OnRecordCmd(true) }
		}
	case dc.RecordCmd == "StopRecord":
		if c.OnRecordCmd != nil {
			return func() { c.OnRecordCmd(false) }
		}
	case dc.GuardCmd == "SetGuard":
		if c.OnGuardCmd != nil {
			return func() { c.OnGuardCmd(true) }
		}
	case dc.GuardCmd == "ResetGuard":
		if c.OnGuardCmd != nil {
			return func() { c.OnGuardCmd(false) }
		}
	case dc.AlarmCmd == "ResetAlarm":
		return c.OnResetAlarm
	case dc.TeleBoot == "Boot":
		return c.OnTeleBoot
	case dc.PTZCmd != "":
		if c.OnPTZCmd != nil {
			return func() { c.OnPTZCmd(dc.PTZCmd) }
		}
	}
	return nil
}

// parseControlSub decodes a DeviceControl body (any recognized
// sub-command or none); false when the body is not a DeviceControl at
// all (queries, notifies and Broadcast route through their own paths).
func parseControlSub(body string) (manscdp.DeviceControl, bool) {
	ct, v, err := manscdp.Decode([]byte(body))
	if err != nil || ct != manscdp.CmdDeviceControl {
		return manscdp.DeviceControl{}, false
	}
	dc, ok := v.(manscdp.DeviceControl)
	if !ok {
		return manscdp.DeviceControl{}, false
	}
	return dc, true
}
