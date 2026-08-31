package device

import "testing"

func TestFormatDeviceIDGolden(t *testing.T) {
	// The default device/channel ID used across this workspace:
	// region 34020000, industry 00, type 132, serial 0000001.
	id, err := FormatDeviceID("34020000", 0, DeviceTypeCamera, 1)
	if err != nil {
		t.Fatalf("format: %v", err)
	}
	if id != "34020000001320000001" {
		t.Fatalf("got %q", id)
	}
}

func TestFormatDeviceIDZeroPads(t *testing.T) {
	id, err := FormatDeviceID("11000000", 6, 5, 42)
	if err != nil {
		t.Fatalf("format: %v", err)
	}
	if id != "11000000060050000042" {
		t.Fatalf("got %q", id)
	}
}

func TestFormatDeviceIDRejectsBadCenterCode(t *testing.T) {
	// Regression: a bad center code is a returned error, not a panic.
	if _, err := FormatDeviceID("3402000", 0, 132, 1); err == nil {
		t.Fatal("short center code must error")
	}
	if _, err := FormatDeviceID("3402000A", 0, 132, 1); err == nil {
		t.Fatal("non-digit center code must error")
	}
	if _, err := FormatDeviceID("", 0, 132, 1); err == nil {
		t.Fatal("empty center code must error")
	}
}

func TestParseDeviceIDRoundtrip(t *testing.T) {
	for _, tc := range []struct {
		industry uint8
		devType  uint16
		serial   uint32
	}{
		{0, 132, 2000001},
		{0, DeviceTypeIPC, 7},
		{6, DeviceTypeNVR, 9999999},
	} {
		id, err := FormatDeviceID("34020000", tc.industry, tc.devType, tc.serial)
		if err != nil {
			t.Fatalf("format: %v", err)
		}
		p, err := ParseDeviceID(id)
		if err != nil {
			t.Fatalf("parse %q: %v", id, err)
		}
		if p.RegionCode != "34020000" || p.IndustryType != tc.industry ||
			p.DeviceType != tc.devType || p.Serial != tc.serial {
			t.Fatalf("roundtrip mismatch: %+v", p)
		}
	}
}

func TestParseDeviceIDRejectsBadInput(t *testing.T) {
	if _, err := ParseDeviceID("3402000000132000000"); err == nil { // 19 digits
		t.Fatal("19-digit ID must error")
	}
	if _, err := ParseDeviceID("340200000013200000011"); err == nil { // 21 digits
		t.Fatal("21-digit ID must error")
	}
	if _, err := ParseDeviceID("3402000A001320000000a"); err == nil {
		t.Fatal("non-digit ID must error")
	}
}

func TestDialogTagIsRandomHexWithoutBranding(t *testing.T) {
	// Regression: the To-tag was "mibee<time>" — branded and predictable.
	// randomTag must be 8 hex chars with no product prefix.
	tag := randomTag()
	if len(tag) != 8 {
		t.Fatalf("tag %q must be 8 chars", tag)
	}
	for _, c := range tag {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			t.Fatalf("tag %q must be lowercase hex", tag)
		}
	}
	if dialogTag == "" || len(dialogTag) != 8 {
		t.Fatalf("package dialogTag %q must be set to 8 hex chars", dialogTag)
	}
}
