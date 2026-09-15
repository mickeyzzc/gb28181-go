package device

// GB/T 28181-2022 §9.3.3 / A.2.3.2 DeviceConfig sub-commands (issue #80,
// twin of gb28181-rs#69): the device decodes the subset a fixed camera
// can act on and fires the matching optional host callback. The answer is
// a Response body with Result (A.2.6.8) — OK when a callback executed,
// the historical reject ERROR otherwise. 校时 is NOT part of this family
// (2022 §9.10.2 does it via the REGISTER response's SIP Date header).

import (
	"strconv"

	"github.com/mickeyzzc/gb28181-go/manscdp"
)

// BasicParamCfg is the A.2.3.2.2 基本参数配置 the host receives — the
// library never hot-applies these; hosts decide what sticks.
type BasicParamCfg struct {
	// Device name ("" = absent).
	Name string
	// Registration expiry in seconds (nil = absent).
	Expiration *uint64
	// Keepalive interval in seconds (nil = absent).
	HeartbeatInterval *uint64
	// Keepalive timeout count (nil = absent).
	HeartbeatCount *uint32
}

func (p BasicParamCfg) String() string {
	name := "-"
	if p.Name != "" {
		name = p.Name
	}
	exp := "-"
	if p.Expiration != nil {
		exp = strconv.FormatUint(*p.Expiration, 10)
	}
	interval := "-"
	if p.HeartbeatInterval != nil {
		interval = strconv.FormatUint(*p.HeartbeatInterval, 10)
	}
	count := "-"
	if p.HeartbeatCount != nil {
		count = strconv.FormatUint(uint64(*p.HeartbeatCount), 10)
	}
	return "name=" + name + " expiration=" + exp +
		" heartbeat_interval=" + interval + " heartbeat_count=" + count
}

// ConfigCallbacks hosts the DeviceConfig sub-commands. Every callback is
// optional — a sub-command without its callback keeps the reject answer.
// Callbacks fire on the SIP receive goroutine — keep them cheap (copy and
// forward into a channel).
type ConfigCallbacks struct {
	// OnBasicParam handles A.2.3.2.2 — device name + registration tuning.
	OnBasicParam func(p BasicParamCfg)
	// OnFrameMirror handles A.2.3.2.9 画面翻转配置 — 0 none, 1 horizontal,
	// 2 vertical, 3 both (A.2.1.22 frameMirrorCfgType).
	OnFrameMirror func(mode uint32)
	// OnAlarmReport handles A.2.3.2.10 报警上报开关配置 — motion-detection /
	// field-detection event report switches (0 off, 1 on).
	OnAlarmReport func(motionDetection, fieldDetection uint32)
}

// callbackFor maps a decoded DeviceConfig to the installed callback.
// Nil means "not handled" — the caller keeps the reject behavior.
func (c *ConfigCallbacks) callbackFor(dc *manscdp.DeviceConfig) func() {
	switch {
	case dc.BasicParam != nil:
		if c.OnBasicParam != nil {
			bp := dc.BasicParam
			return func() {
				c.OnBasicParam(BasicParamCfg{
					Name:              bp.Name,
					Expiration:        bp.Expiration,
					HeartbeatInterval: bp.HeartBeatInterval,
					HeartbeatCount:    bp.HeartBeatCount,
				})
			}
		}
	case dc.FrameMirror != nil:
		if c.OnFrameMirror != nil {
			mode := *dc.FrameMirror
			return func() { c.OnFrameMirror(mode) }
		}
	case dc.AlarmReport != nil:
		if c.OnAlarmReport != nil {
			ar := dc.AlarmReport
			return func() { c.OnAlarmReport(ar.MotionDetection, ar.FieldDetection) }
		}
	}
	return nil
}

// parseDeviceConfigSub decodes a DeviceConfig body carrying a recognized
// sub-command; false when the body is not a DeviceConfig or carries none
// of the decodable subset (those keep the reject behavior).
func parseDeviceConfigSub(body string) (manscdp.DeviceConfig, bool) {
	ct, v, err := manscdp.Decode([]byte(body))
	if err != nil || ct != manscdp.CmdDeviceConfig {
		return manscdp.DeviceConfig{}, false
	}
	dc, ok := v.(manscdp.DeviceConfig)
	if !ok {
		return manscdp.DeviceConfig{}, false
	}
	if dc.BasicParam == nil && dc.FrameMirror == nil && dc.AlarmReport == nil {
		return manscdp.DeviceConfig{}, false
	}
	return dc, true
}
