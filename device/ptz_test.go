package device_test

// Bit-level PTZCmd decode tests (issue #80 / gb28181-rs#57 remainder): the
// golden hex strings are the byte-exact outputs of the platform-side
// builders (platform.BuildPTZCommand / BuildPTZPresetCommand /
// BuildPTZCruiseCommand / BuildFICommand / BuildAuxSwitchCommand,
// GB/T 28181 §A.3-A.4) — the device decodes exactly what platforms build,
// and the round-trip test keeps the two sides pinned together. The same
// hex table is the Rust twin's golden.

import (
	"bytes"
	"sync"
	"testing"
	"time"

	"github.com/mickeyzzc/gb28181-go/device"
	"github.com/mickeyzzc/gb28181-go/platform"
)

func TestDecodePTZCommandGoldens(t *testing.T) {
	tests := []struct {
		name string
		hex  string
		want device.PtzCommand
	}{
		// platform.BuildPTZCommand(direction, 0x20) outputs.
		{"stop", "A50F0100000000B5", device.PtzCommand{
			Kind: device.PtzMove, Raw: [8]byte{0xA5, 0x0F, 0x01, 0x00, 0x00, 0x00, 0x00, 0xB5},
		}},
		{"up", "A50F0108002000DD", device.PtzCommand{
			Kind: device.PtzMove, Bits: device.PtzUp, TiltSpeed: 0x20,
			Raw: [8]byte{0xA5, 0x0F, 0x01, 0x08, 0x00, 0x20, 0x00, 0xDD},
		}},
		{"down", "A50F0104002000D9", device.PtzCommand{
			Kind: device.PtzMove, Bits: device.PtzDown, TiltSpeed: 0x20,
			Raw: [8]byte{0xA5, 0x0F, 0x01, 0x04, 0x00, 0x20, 0x00, 0xD9},
		}},
		{"left", "A50F0102200000D7", device.PtzCommand{
			Kind: device.PtzMove, Bits: device.PtzLeft, PanSpeed: 0x20,
			Raw: [8]byte{0xA5, 0x0F, 0x01, 0x02, 0x20, 0x00, 0x00, 0xD7},
		}},
		{"right", "A50F0101200000D6", device.PtzCommand{
			Kind: device.PtzMove, Bits: device.PtzRight, PanSpeed: 0x20,
			Raw: [8]byte{0xA5, 0x0F, 0x01, 0x01, 0x20, 0x00, 0x00, 0xD6},
		}},
		{"up-left", "A50F010A202000FF", device.PtzCommand{
			Kind: device.PtzMove, Bits: device.PtzUp | device.PtzLeft, PanSpeed: 0x20, TiltSpeed: 0x20,
			Raw: [8]byte{0xA5, 0x0F, 0x01, 0x0A, 0x20, 0x20, 0x00, 0xFF},
		}},
		{"up-right", "A50F0109202000FE", device.PtzCommand{
			Kind: device.PtzMove, Bits: device.PtzUp | device.PtzRight, PanSpeed: 0x20, TiltSpeed: 0x20,
			Raw: [8]byte{0xA5, 0x0F, 0x01, 0x09, 0x20, 0x20, 0x00, 0xFE},
		}},
		{"down-left", "A50F0106202000FB", device.PtzCommand{
			Kind: device.PtzMove, Bits: device.PtzDown | device.PtzLeft, PanSpeed: 0x20, TiltSpeed: 0x20,
			Raw: [8]byte{0xA5, 0x0F, 0x01, 0x06, 0x20, 0x20, 0x00, 0xFB},
		}},
		{"down-right", "A50F0105202000FA", device.PtzCommand{
			Kind: device.PtzMove, Bits: device.PtzDown | device.PtzRight, PanSpeed: 0x20, TiltSpeed: 0x20,
			Raw: [8]byte{0xA5, 0x0F, 0x01, 0x05, 0x20, 0x20, 0x00, 0xFA},
		}},
		{"zoom-in", "A50F0110000020E5", device.PtzCommand{
			Kind: device.PtzMove, Bits: device.PtzZoomIn, ZoomSpeed: 0x20,
			Raw: [8]byte{0xA5, 0x0F, 0x01, 0x10, 0x00, 0x00, 0x20, 0xE5},
		}},
		{"zoom-out", "A50F0120000020F5", device.PtzCommand{
			Kind: device.PtzMove, Bits: device.PtzZoomOut, ZoomSpeed: 0x20,
			Raw: [8]byte{0xA5, 0x0F, 0x01, 0x20, 0x00, 0x00, 0x20, 0xF5},
		}},
		// platform.BuildPTZPresetCommand(action, 5) outputs.
		{"preset set", "A50F01810000053B", device.PtzCommand{
			Kind: device.PtzPreset, PresetAction: device.PtzPresetSet, Preset: 5,
			Raw: [8]byte{0xA5, 0x0F, 0x01, 0x81, 0x00, 0x00, 0x05, 0x3B},
		}},
		{"preset call", "A50F01820000053C", device.PtzCommand{
			Kind: device.PtzPreset, PresetAction: device.PtzPresetCall, Preset: 5,
			Raw: [8]byte{0xA5, 0x0F, 0x01, 0x82, 0x00, 0x00, 0x05, 0x3C},
		}},
		{"preset delete", "A50F01830000053D", device.PtzCommand{
			Kind: device.PtzPreset, PresetAction: device.PtzPresetDelete, Preset: 5,
			Raw: [8]byte{0xA5, 0x0F, 0x01, 0x83, 0x00, 0x00, 0x05, 0x3D},
		}},
		// platform.BuildPTZCruiseCommand(action, 2, 7) outputs.
		{"cruise add-point", "A50F018402000742", device.PtzCommand{
			Kind: device.PtzCruise, CruiseAction: device.PtzCruiseAddPoint, CruiseGroup: 2, CruiseValue: 7,
			Raw: [8]byte{0xA5, 0x0F, 0x01, 0x84, 0x02, 0x00, 0x07, 0x42},
		}},
		{"cruise del-point", "A50F018502000743", device.PtzCommand{
			Kind: device.PtzCruise, CruiseAction: device.PtzCruiseDelPoint, CruiseGroup: 2, CruiseValue: 7,
			Raw: [8]byte{0xA5, 0x0F, 0x01, 0x85, 0x02, 0x00, 0x07, 0x43},
		}},
		{"cruise speed", "A50F018602000744", device.PtzCommand{
			Kind: device.PtzCruise, CruiseAction: device.PtzCruiseSpeed, CruiseGroup: 2, CruiseValue: 7,
			Raw: [8]byte{0xA5, 0x0F, 0x01, 0x86, 0x02, 0x00, 0x07, 0x44},
		}},
		{"cruise stay", "A50F018702000745", device.PtzCommand{
			Kind: device.PtzCruise, CruiseAction: device.PtzCruiseStay, CruiseGroup: 2, CruiseValue: 7,
			Raw: [8]byte{0xA5, 0x0F, 0x01, 0x87, 0x02, 0x00, 0x07, 0x45},
		}},
		{"cruise start", "A50F018802000746", device.PtzCommand{
			Kind: device.PtzCruise, CruiseAction: device.PtzCruiseStart, CruiseGroup: 2, CruiseValue: 7,
			Raw: [8]byte{0xA5, 0x0F, 0x01, 0x88, 0x02, 0x00, 0x07, 0x46},
		}},
		// platform.BuildFICommand(action, 0x40) outputs (§A.3.3).
		{"fi iris-close", "A50F01480040003D", device.PtzCommand{
			Kind: device.PtzLens, Bits: device.PtzIrisClose, IrisSpeed: 0x40,
			Raw: [8]byte{0xA5, 0x0F, 0x01, 0x48, 0x00, 0x40, 0x00, 0x3D},
		}},
		{"fi iris-open", "A50F014400400039", device.PtzCommand{
			Kind: device.PtzLens, Bits: device.PtzIrisOpen, IrisSpeed: 0x40,
			Raw: [8]byte{0xA5, 0x0F, 0x01, 0x44, 0x00, 0x40, 0x00, 0x39},
		}},
		{"fi focus-near", "A50F014240000037", device.PtzCommand{
			Kind: device.PtzLens, Bits: device.PtzFocusNear, FocusSpeed: 0x40,
			Raw: [8]byte{0xA5, 0x0F, 0x01, 0x42, 0x40, 0x00, 0x00, 0x37},
		}},
		{"fi focus-far", "A50F014140000036", device.PtzCommand{
			Kind: device.PtzLens, Bits: device.PtzFocusFar, FocusSpeed: 0x40,
			Raw: [8]byte{0xA5, 0x0F, 0x01, 0x41, 0x40, 0x00, 0x00, 0x36},
		}},
		{"fi stop", "A50F0140000000F5", device.PtzCommand{
			Kind: device.PtzLens,
			Raw:  [8]byte{0xA5, 0x0F, 0x01, 0x40, 0x00, 0x00, 0x00, 0xF5},
		}},
		// platform.BuildAuxSwitchCommand(1, on) outputs (§A.3.7).
		{"aux on", "A50F018C01000042", device.PtzCommand{
			Kind: device.PtzAuxSwitch, SwitchNumber: 1, On: true,
			Raw: [8]byte{0xA5, 0x0F, 0x01, 0x8C, 0x01, 0x00, 0x00, 0x42},
		}},
		{"aux off", "A50F018D01000043", device.PtzCommand{
			Kind: device.PtzAuxSwitch, SwitchNumber: 1, On: false,
			Raw: [8]byte{0xA5, 0x0F, 0x01, 0x8D, 0x01, 0x00, 0x00, 0x43},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			want := tt.want
			want.RawHex = tt.hex
			got := device.DecodePTZCommand(tt.hex)
			if got != want {
				t.Fatalf("DecodePTZCommand(%q) = %+v, want %+v", tt.hex, got, want)
			}
		})
	}
}

// Round-trip against the live platform builders: decode(builder()) must
// classify for every direction, preset, cruise, FI and aux action — the
// encode/decode pair can never drift apart silently.
func TestDecodePTZCommandRoundTripWithBuilders(t *testing.T) {
	wantBits := map[string]byte{
		platform.DirStop:      0,
		platform.DirUp:        device.PtzUp,
		platform.DirDown:      device.PtzDown,
		platform.DirLeft:      device.PtzLeft,
		platform.DirRight:     device.PtzRight,
		platform.DirUpLeft:    device.PtzUp | device.PtzLeft,
		platform.DirUpRight:   device.PtzUp | device.PtzRight,
		platform.DirDownLeft:  device.PtzDown | device.PtzLeft,
		platform.DirDownRight: device.PtzDown | device.PtzRight,
		platform.DirZoomIn:    device.PtzZoomIn,
		platform.DirZoomOut:   device.PtzZoomOut,
	}
	for d, bits := range wantBits {
		cmd, err := platform.BuildPTZCommand(d, 0x20)
		if err != nil {
			t.Fatalf("build %s: %v", d, err)
		}
		got := device.DecodePTZCommand(hexOf(cmd))
		if got.Kind != device.PtzMove {
			t.Fatalf("%s: kind = %v, want PtzMove", d, got.Kind)
		}
		if got.Bits != bits {
			t.Fatalf("%s: bits = %#x, want %#x", d, got.Bits, bits)
		}
		if got.IsStop() != (bits == 0) {
			t.Fatalf("%s: IsStop() = %v, want %v", d, got.IsStop(), bits == 0)
		}
		if !bytes.Equal(got.Raw[:], cmd) {
			t.Fatalf("%s: raw = % X, want % X", d, got.Raw, cmd)
		}
	}
	for _, a := range []struct {
		action string
		want   device.PtzPresetAction
	}{
		{platform.PresetSet, device.PtzPresetSet},
		{platform.PresetCall, device.PtzPresetCall},
		{platform.PresetDelete, device.PtzPresetDelete},
	} {
		cmd, err := platform.BuildPTZPresetCommand(a.action, 9)
		if err != nil {
			t.Fatalf("build preset %s: %v", a.action, err)
		}
		got := device.DecodePTZCommand(hexOf(cmd))
		if got.Kind != device.PtzPreset || got.PresetAction != a.want || got.Preset != 9 {
			t.Fatalf("preset %s: got %+v", a.action, got)
		}
	}
	for _, a := range []struct {
		action string
		want   device.PtzCruiseAction
	}{
		{platform.CruiseAddPoint, device.PtzCruiseAddPoint},
		{platform.CruiseDelPoint, device.PtzCruiseDelPoint},
		{platform.CruiseSpeed, device.PtzCruiseSpeed},
		{platform.CruiseStay, device.PtzCruiseStay},
		{platform.CruiseStart, device.PtzCruiseStart},
	} {
		cmd, err := platform.BuildPTZCruiseCommand(a.action, 3, 11)
		if err != nil {
			t.Fatalf("build cruise %s: %v", a.action, err)
		}
		got := device.DecodePTZCommand(hexOf(cmd))
		if got.Kind != device.PtzCruise || got.CruiseAction != a.want ||
			got.CruiseGroup != 3 || got.CruiseValue != 11 {
			t.Fatalf("cruise %s: got %+v", a.action, got)
		}
	}
	for _, a := range []struct {
		action string
		bits   byte
	}{
		{platform.FIIrisClose, device.PtzIrisClose},
		{platform.FIIrisOpen, device.PtzIrisOpen},
		{platform.FIFocusNear, device.PtzFocusNear},
		{platform.FIFocusFar, device.PtzFocusFar},
		{platform.FILensStop, 0},
	} {
		cmd, err := platform.BuildFICommand(a.action, 0x40)
		if err != nil {
			t.Fatalf("build fi %s: %v", a.action, err)
		}
		got := device.DecodePTZCommand(hexOf(cmd))
		if got.Kind != device.PtzLens || got.Bits != a.bits {
			t.Fatalf("fi %s: got %+v", a.action, got)
		}
	}
	for _, on := range []bool{true, false} {
		cmd, err := platform.BuildAuxSwitchCommand(2, on)
		if err != nil {
			t.Fatalf("build aux %v: %v", on, err)
		}
		got := device.DecodePTZCommand(hexOf(cmd))
		if got.Kind != device.PtzAuxSwitch || got.SwitchNumber != 2 || got.On != on {
			t.Fatalf("aux %v: got %+v", on, got)
		}
	}
}

func TestDecodePTZCommandInvalid(t *testing.T) {
	for _, tt := range []struct {
		name string
		hex  string
	}{
		{"too short", "A50F01"},
		{"too long", "A50F0100000000B500"},
		{"odd length", "A50F0100000000B"},
		{"not hex", "ZZ0F0100000000B5"},
		{"wrong start byte", "950F0100000000B5"},
		{"checksum mismatch", "A50F0100000000FF"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := device.DecodePTZCommand(tt.hex)
			if got.Kind != device.PtzInvalid {
				t.Fatalf("kind = %v, want PtzInvalid", got.Kind)
			}
			if got.RawHex != tt.hex {
				t.Fatalf("RawHex = %q, want the untouched input %q", got.RawHex, tt.hex)
			}
		})
	}

	// Lowercase hex is accepted (vendors emit both cases).
	got := device.DecodePTZCommand("a50f0108002000dd")
	if got.Kind != device.PtzMove || got.Bits != device.PtzUp || got.TiltSpeed != 0x20 {
		t.Fatalf("lowercase decode = %+v", got)
	}
	// Surrounding whitespace is tolerated (XML indentation).
	got = device.DecodePTZCommand(" A50F0108002000DD ")
	if got.Kind != device.PtzMove || got.Bits != device.PtzUp {
		t.Fatalf("padded decode = %+v", got)
	}
	// Structurally valid but unrecognized instruction code → Unknown with
	// the data bytes preserved for host-side vendor extensions.
	// Checksum: A5+0F+01+99+11+22+33 = 0x1B4 & 0xFF = 0xB4.
	got = device.DecodePTZCommand("A50F0199112233B4")
	if got.Kind != device.PtzUnknown || got.Code != 0x99 || got.Data != [3]byte{0x11, 0x22, 0x33} {
		t.Fatalf("unknown-code decode = %+v", got)
	}
}

// hexOf formats 8 bytes as continuous uppercase hex (the PTZCmd wire form).
func hexOf(b []byte) string {
	const digits = "0123456789ABCDEF"
	out := make([]byte, 0, 16)
	for _, v := range b {
		out = append(out, digits[v>>4], digits[v&0x0F])
	}
	return string(out)
}

// Wire-level: a DeviceControl(PTZCmd) MESSAGE delivers the decoded
// command to OnPTZCmd — and undecodable hex is still delivered as
// PtzInvalid (the command total, no new reject path).
func TestDeviceControlPTZCmdDecoded(t *testing.T) {
	var mu sync.Mutex
	var got device.PtzCommand
	platConn, devAddr := startWireTestServer(t, func(srv *device.Server) {
		srv.SetControlHandlers(device.ControlCallbacks{
			OnPTZCmd: func(cmd device.PtzCommand) {
				mu.Lock()
				got = cmd
				mu.Unlock()
			},
		})
	})

	writeSnapMsg(t, platConn, ctrlMessage("ptz1", "<PTZCmd>A50F0102200000D7</PTZCmd>"), devAddr)
	resp, _ := readSnapMsg(t, platConn)
	if resp.StatusCode != 200 {
		t.Fatalf("control reply status = %d, want 200", resp.StatusCode)
	}
	waitFor(t, 5*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return got.Kind == device.PtzMove
	})
	mu.Lock()
	if got.Bits != device.PtzLeft || got.PanSpeed != 0x20 || got.RawHex != "A50F0102200000D7" {
		mu.Unlock()
		t.Fatalf("decoded command = %+v, want left @0x20", got)
	}
	mu.Unlock()

	writeSnapMsg(t, platConn, ctrlMessage("ptz2", "<PTZCmd>1234</PTZCmd>"), devAddr)
	resp, _ = readSnapMsg(t, platConn)
	if resp.StatusCode != 200 {
		t.Fatalf("invalid-hex control reply status = %d, want 200", resp.StatusCode)
	}
	waitFor(t, 5*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return got.Kind == device.PtzInvalid
	})
	mu.Lock()
	if got.RawHex != "1234" {
		mu.Unlock()
		t.Fatalf("invalid delivery = %+v, want PtzInvalid with raw 1234", got)
	}
	mu.Unlock()
}
