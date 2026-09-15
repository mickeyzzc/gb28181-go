package device

// Bit-level PTZCmd decode (GB/T 28181 §A.3-A.4; issue #80 / gb28181-rs#57
// remainder). The 8-byte A5 0F command carried in <PTZCmd> is decoded
// into a structured command for the host — the byte-exact inverse of the
// platform-side builders (platform.BuildPTZCommand & family): decode
// round-trips every builder output, and the golden hex table here is the
// Rust twin's golden too.

import (
	"encoding/hex"
	"strings"
)

// PtzKind classifies a decoded PTZCmd instruction.
type PtzKind int

const (
	// PtzMove is a §A.4 pan/tilt/zoom movement (Bits 0 = stop).
	PtzMove PtzKind = iota + 1
	// PtzPreset is a §A.3.4 preset set/call/delete.
	PtzPreset
	// PtzCruise is a §A.3.5 cruise instruction.
	PtzCruise
	// PtzLens is a §A.3.3 FI focus/iris instruction.
	PtzLens
	// PtzAuxSwitch is a §A.3.7 auxiliary switch (wiper/light) toggle.
	PtzAuxSwitch
	// PtzUnknown is structurally valid but carries an unrecognized
	// instruction code — delivered verbatim for vendor extensions.
	PtzUnknown
	// PtzInvalid failed structural validation (hex length, A5 start byte,
	// or checksum). RawHex preserves the input as received.
	PtzInvalid
)

// PTZ direction bits (§A.4, byte 4 of the command). Diagonals combine two
// axis bits; zoom rides the high bits of the same byte.
const (
	PtzRight   byte = 0x01
	PtzLeft    byte = 0x02
	PtzDown    byte = 0x04
	PtzUp      byte = 0x08
	PtzZoomIn  byte = 0x10
	PtzZoomOut byte = 0x20
)

// FI lens action bits (§A.3.3 表 A.6 — low nibble of byte 4, which the
// builders OR with 0x40).
const (
	PtzFocusFar  byte = 0x01
	PtzFocusNear byte = 0x02
	PtzIrisOpen  byte = 0x04
	PtzIrisClose byte = 0x08
)

// PtzPresetAction enumerates the §A.3.4 preset instructions.
type PtzPresetAction int

const (
	PtzPresetSet    PtzPresetAction = iota // 设置预置位 (0x81)
	PtzPresetCall                          // 调用预置位 (0x82)
	PtzPresetDelete                        // 删除预置位 (0x83)
)

// PtzCruiseAction enumerates the §A.3.5 cruise instructions.
type PtzCruiseAction int

const (
	PtzCruiseAddPoint PtzCruiseAction = iota // 加入巡航点 (0x84)
	PtzCruiseDelPoint                        // 删除巡航点 (0x85)
	PtzCruiseSpeed                           // 设置巡航速度 (0x86)
	PtzCruiseStay                            // 设置巡航停留时间 (0x87)
	PtzCruiseStart                           // 开始巡航 (0x88)
)

// PtzCommand is the decoded 8-byte PTZCmd. Which fields carry meaning
// depends on Kind; Raw and RawHex are always set (except Raw on
// PtzInvalid, where the input never reached 8 valid bytes).
type PtzCommand struct {
	Kind   PtzKind
	Bits   byte // Move: direction bits; Lens: FI bits (low nibble)
	Raw    [8]byte
	RawHex string // the command as received (trimmed), any case

	// Move (§A.4): speed of each acting axis, 0x00-0xFF slow→fast.
	PanSpeed  byte
	TiltSpeed byte
	ZoomSpeed byte

	// Preset (§A.3.4): number 1-255 rides in byte 7.
	PresetAction PtzPresetAction
	Preset       byte

	// Cruise (§A.3.5): group in byte 5, value (preset/speed/stay) in
	// byte 7.
	CruiseAction PtzCruiseAction
	CruiseGroup  byte
	CruiseValue  byte

	// Lens (§A.3.3): focus speed in byte 5, iris speed in byte 6.
	FocusSpeed byte
	IrisSpeed  byte

	// AuxSwitch (§A.3.7): switch number in byte 5.
	SwitchNumber byte
	On           bool

	// Unknown: the unrecognized instruction code and data bytes 5-7.
	Code byte
	Data [3]byte
}

// IsStop reports whether the command is a §A.4 stop (a Move with no
// direction bits set).
func (c PtzCommand) IsStop() bool {
	return c.Kind == PtzMove && c.Bits == 0
}

// Ptz instruction codes (byte 4 of the command).
const (
	ptzCodePresetBase   byte = 0x81 // 0x81-0x83 preset set/call/delete
	ptzCodeCruiseBase   byte = 0x84 // 0x84-0x88 cruise instructions
	ptzCodeLensMarker   byte = 0x40 // byte4 high nibble marking FI lens cmds
	ptzCodeLensMask     byte = 0xF0
	ptzCodeAuxSwitchOn  byte = 0x8C
	ptzCodeAuxSwitchOff byte = 0x8D
	ptzCodeMoveMax      byte = 0x3F // direction/zoom bits live at 0x00-0x3F
	ptzPtzStart         byte = 0xA5
)

// DecodePTZCommand decodes the <PTZCmd> hex payload into a structured
// command. It is total: structurally invalid input (wrong length,
// non-hex, missing A5 start byte, checksum mismatch) yields
// Kind=PtzInvalid with RawHex preserving the input, never an error —
// hosts keep seeing everything the platform sent. Bytes 2-3 (the 0F
// combination byte and address) vary across vendors and are validated
// only as checksum input.
func DecodePTZCommand(a505Hex string) PtzCommand {
	trimmed := strings.TrimSpace(a505Hex)
	cmd := PtzCommand{Kind: PtzInvalid, RawHex: trimmed}
	raw, err := hex.DecodeString(trimmed)
	if err != nil || len(raw) != 8 {
		return cmd
	}
	if raw[0] != ptzPtzStart {
		return cmd
	}
	var sum byte
	for _, b := range raw[:7] {
		sum += b
	}
	if sum != raw[7] {
		return cmd
	}
	copy(cmd.Raw[:], raw)

	code := raw[3]
	switch {
	case code <= ptzCodeMoveMax:
		cmd.Kind = PtzMove
		cmd.Bits = code
		cmd.PanSpeed = raw[4]
		cmd.TiltSpeed = raw[5]
		cmd.ZoomSpeed = raw[6]
	case code&ptzCodeLensMask == ptzCodeLensMarker:
		cmd.Kind = PtzLens
		cmd.Bits = code & 0x0F
		cmd.FocusSpeed = raw[4]
		cmd.IrisSpeed = raw[5]
	case code >= ptzCodePresetBase && code <= ptzCodePresetBase+2:
		cmd.Kind = PtzPreset
		cmd.PresetAction = PtzPresetAction(code - ptzCodePresetBase)
		cmd.Preset = raw[6]
	case code >= ptzCodeCruiseBase && code <= ptzCodeCruiseBase+4:
		cmd.Kind = PtzCruise
		cmd.CruiseAction = PtzCruiseAction(code - ptzCodeCruiseBase)
		cmd.CruiseGroup = raw[4]
		cmd.CruiseValue = raw[6]
	case code == ptzCodeAuxSwitchOn || code == ptzCodeAuxSwitchOff:
		cmd.Kind = PtzAuxSwitch
		cmd.SwitchNumber = raw[4]
		cmd.On = code == ptzCodeAuxSwitchOn
	default:
		cmd.Kind = PtzUnknown
		cmd.Code = code
		cmd.Data = [3]byte{raw[4], raw[5], raw[6]}
	}
	return cmd
}
