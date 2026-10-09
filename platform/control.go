package platform

// Non-PTZ DeviceControl instructions (GB/T 28181-2016 § 9.3.2, #379):
// remote device-side record start/stop, arm/disarm, alarm reset, reboot,
// and home-position reset. Sent as Control messages via the PTZController's
// channel-resolution + SIP MESSAGE plumbing.

import (
	"fmt"
	"strconv"

	"github.com/mickeyzzc/gb28181-go/manscdp"
)

// Valid RecordCmd values.
const (
	RecordCmdStart = "Record"
	RecordCmdStop  = "StopRecord"
)

// Valid GuardCmd values.
const (
	GuardCmdSet   = "SetGuard"
	GuardCmdReset = "ResetGuard"
)

// SendDeviceControl resolves channelID across registered devices and sends a
// non-PTZ DeviceControl. element is the XML child carrying the command
// ("RecordCmd"/"GuardCmd"/"AlarmCmd"/"TeleBoot"); value its text content
// ("" for flag-style elements). HomePosition has its own wire form — use
// SetHomePosition.
func (c *PTZController) SendDeviceControl(channelID, element, value string) error {
	ch, dev, err := c.locateChannel(channelID)
	if err != nil {
		return err
	}
	if dev.Status.Load() != DeviceOnline {
		return ErrDeviceOffline
	}

	dc := manscdp.DeviceControl{
		CmdType:  manscdp.CmdDeviceControl,
		SN:       int(c.seq.Add(1)),
		DeviceID: ch.ID,
	}
	switch element {
	case "RecordCmd":
		dc.RecordCmd = value
	case "GuardCmd":
		dc.GuardCmd = value
	case "AlarmCmd":
		dc.AlarmCmd = value
	case "TeleBoot":
		dc.TeleBoot = value
	case "IFrameCmd":
		// Force the next encoded frame to be an IDR (§9.3.2; the only
		// valid value is "Send"). Mirrors gb28181-rs #58.
		if value != "Send" {
			return fmt.Errorf("gb28181: IFrameCmd value must be %q, got %q", "Send", value)
		}
		dc.IFrameCmd = value
	case "FormatSDCard":
		// A.2.3.1.13: the element is an integer card number (>=1; 0 =
		// every card) — the generic string-valued path carries it.
		card, err := strconv.Atoi(value)
		if err != nil || card < 0 {
			return fmt.Errorf("gb28181: FormatSDCard must be a non-negative integer, got %q", value)
		}
		dc.FormatSDCard = &card
	default:
		return fmt.Errorf("gb28181: unsupported DeviceControl element %q", element)
	}
	body, err := manscdp.Encode(dc)
	if err != nil {
		return fmt.Errorf("gb28181: encode DeviceControl: %w", err)
	}
	if err := c.sender.SendMessage(ch.DeviceID, body); err != nil {
		return fmt.Errorf("gb28181: send DeviceControl to %s: %w", ch.DeviceID, err)
	}
	return nil
}

// SetHomePosition sends the 看守位 control (A.2.3.1.10): auto-return to
// presetIdx after resetSecs seconds of inactivity (enabled=false sends
// Enabled=0). Nil optional fields mean "keep current". Replaces the old
// string-form element, which never matched the standard's wire shape.
func (c *PTZController) SetHomePosition(channelID string, enabled bool, resetSecs, presetIdx *uint32) error {
	ch, dev, err := c.locateChannel(channelID)
	if err != nil {
		return err
	}
	if dev.Status.Load() != DeviceOnline {
		return ErrDeviceOffline
	}

	hp := manscdp.HomePositionCmd{ResetTime: resetSecs, PresetIndex: presetIdx}
	if enabled {
		hp.Enabled = 1
	}
	dc := manscdp.DeviceControl{
		CmdType:      manscdp.CmdDeviceControl,
		SN:           int(c.seq.Add(1)),
		DeviceID:     ch.ID,
		HomePosition: &hp,
	}
	body, err := manscdp.Encode(dc)
	if err != nil {
		return fmt.Errorf("gb28181: encode DeviceControl: %w", err)
	}
	if err := c.sender.SendMessage(ch.DeviceID, body); err != nil {
		return fmt.Errorf("gb28181: send DeviceControl to %s: %w", ch.DeviceID, err)
	}
	return nil
}

// SendSnapShotCmd issues the GB/T 28181-2022 image-snapshot
// configuration (A.2.1.24, issue #107): the device captures SnapNum JPEGs
// (Interval seconds apart) and POSTs them to UploadURL, then reports
// UploadSnapShotFinished with the same SessionID. The command rides the
// device-config channel — Control root, CmdType DeviceConfig,
// <SnapShotConfig> payload.
func (c *PTZController) SendSnapShotCmd(channelID string, cmd manscdp.SnapShotCmd) error {
	ch, dev, err := c.locateChannel(channelID)
	if err != nil {
		return err
	}
	if dev.Status.Load() != DeviceOnline {
		return ErrDeviceOffline
	}

	cfg := manscdp.DeviceConfig{
		CmdType:        manscdp.CmdDeviceConfig,
		SN:             int(c.seq.Add(1)),
		DeviceID:       ch.ID,
		SnapShotConfig: &cmd,
	}
	body, err := manscdp.Encode(cfg)
	if err != nil {
		return fmt.Errorf("gb28181: encode DeviceConfig: %w", err)
	}
	if err := c.sender.SendMessage(ch.DeviceID, body); err != nil {
		return fmt.Errorf("gb28181: send DeviceConfig to %s: %w", ch.DeviceID, err)
	}
	return nil
}

// SendDeviceUpgradeCmd issues the GB/T 28181-2022 firmware-upgrade
// control (A.2.3.1.12, issue #108): the device downloads FileURL, applies
// the firmware, and reports DeviceUpgradeResult (A.2.5.9) with the same
// SessionID.
func (c *PTZController) SendDeviceUpgradeCmd(channelID string, cmd manscdp.DeviceUpgradeCmd) error {
	ch, dev, err := c.locateChannel(channelID)
	if err != nil {
		return err
	}
	if dev.Status.Load() != DeviceOnline {
		return ErrDeviceOffline
	}

	dc := manscdp.DeviceControl{
		CmdType:       manscdp.CmdDeviceControl,
		SN:            int(c.seq.Add(1)),
		DeviceID:      ch.ID,
		DeviceUpgrade: &cmd,
	}
	body, err := manscdp.Encode(dc)
	if err != nil {
		return fmt.Errorf("gb28181: encode DeviceControl: %w", err)
	}
	if err := c.sender.SendMessage(ch.DeviceID, body); err != nil {
		return fmt.Errorf("gb28181: send DeviceControl to %s: %w", ch.DeviceID, err)
	}
	return nil
}

// SendFormatSDCardCmd issues the A.2.3.1.13 storage-card format control:
// card numbers start at 1; 0 formats every card.
func (c *PTZController) SendFormatSDCardCmd(channelID string, card int) error {
	return c.SendDeviceControl(channelID, "FormatSDCard", strconv.Itoa(card))
}

// SendPTZPreciseCmd issues the A.2.3.1.11 PTZ precise control: absolute
// Pan/Tilt/Zoom angles, every field optional.
func (c *PTZController) SendPTZPreciseCmd(channelID string, p manscdp.PTZPreciseCmd) error {
	ch, dev, err := c.locateChannel(channelID)
	if err != nil {
		return err
	}
	if dev.Status.Load() != DeviceOnline {
		return ErrDeviceOffline
	}

	dc := manscdp.DeviceControl{
		CmdType:        manscdp.CmdDeviceControl,
		SN:             int(c.seq.Add(1)),
		DeviceID:       ch.ID,
		PTZPreciseCtrl: &p,
	}
	body, err := manscdp.Encode(dc)
	if err != nil {
		return fmt.Errorf("gb28181: encode DeviceControl: %w", err)
	}
	if err := c.sender.SendMessage(ch.DeviceID, body); err != nil {
		return fmt.Errorf("gb28181: send DeviceControl to %s: %w", ch.DeviceID, err)
	}
	return nil
}

// StartManualRecord commands the channel to start manual recording
// (DeviceControl RecordCmd=Record, GB/T 28181 §9.3.2).
func (c *PTZController) StartManualRecord(channelID string) error {
	return c.SendDeviceControl(channelID, "RecordCmd", "Record")
}

// StopManualRecord commands the channel to stop manual recording
// (DeviceControl RecordCmd=StopRecord).
func (c *PTZController) StopManualRecord(channelID string) error {
	return c.SendDeviceControl(channelID, "RecordCmd", "StopRecord")
}

// SendIFrameCmd forces the device's next encoded frame to be an IDR
// (GB/T 28181 §9.3.2, issue #81) — platforms send this when starting a
// pull or after packet loss.
func (c *PTZController) SendIFrameCmd(channelID string) error {
	return c.SendDeviceControl(channelID, "IFrameCmd", "Send")
}
