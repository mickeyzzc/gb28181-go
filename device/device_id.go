// GB/T 28181 device ID formatting and parsing.
//
// GB28181 device IDs are 20-digit codes with the structure:
//
//	[center 8 digits][industry 2 digits][type 3 digits][serial 7 digits]
//
// Region codes follow GB/T 2260 (administrative division codes of China);
// type codes identify the device type (111 = IPC, 132 = camera, …). This
// mirrors the Rust twin's gb28181-rs device_id module byte-for-byte.

package device

import "fmt"

// DeviceIDParts is a parsed 20-digit GB/T 28181 device ID.
type DeviceIDParts struct {
	// RegionCode is the 8-digit administrative region code (GB/T 2260).
	RegionCode string
	// IndustryType is the 2-digit industry type code.
	IndustryType uint8
	// DeviceType is the 3-digit device type code.
	DeviceType uint16
	// Serial is the 7-digit serial number.
	Serial uint32
}

// Standard device type codes used in GB/T 28181.
const (
	// DeviceTypeIPC is a front-end device (IPC, camera).
	DeviceTypeIPC uint16 = 111
	// DeviceTypeCamera is a camera (the 132 used across this workspace).
	DeviceTypeCamera uint16 = 132
	// DeviceTypeNVR is an NVR / DVR.
	DeviceTypeNVR uint16 = 118
	// DeviceTypeAlarm is an alarm device.
	DeviceTypeAlarm uint16 = 122
)

// FormatDeviceID builds a 20-digit GB/T 28181 device ID from its parts.
// Returns an error when centerCode is not exactly 8 ASCII digits — this is
// a public API, bad input is returned rather than panicked on.
func FormatDeviceID(centerCode string, industry uint8, devType uint16, serial uint32) (string, error) {
	if len(centerCode) != 8 {
		return "", fmt.Errorf("center code must be exactly 8 ASCII digits, got %q (%d chars)", centerCode, len(centerCode))
	}
	for _, c := range centerCode {
		if c < '0' || c > '9' {
			return "", fmt.Errorf("center code must contain only ASCII digits, got %q", centerCode)
		}
	}
	return fmt.Sprintf("%s%02d%03d%07d", centerCode, industry, devType, serial), nil
}

// ParseDeviceID parses a 20-digit GB/T 28181 device ID into its parts.
func ParseDeviceID(id string) (DeviceIDParts, error) {
	if len(id) != 20 {
		return DeviceIDParts{}, fmt.Errorf("device ID must be exactly 20 digits, got %d chars", len(id))
	}
	for _, c := range id {
		if c < '0' || c > '9' {
			return DeviceIDParts{}, fmt.Errorf("device ID must contain only ASCII digits, got %q", id)
		}
	}
	var p DeviceIDParts
	p.RegionCode = id[0:8]
	if _, err := fmt.Sscanf(id[8:10], "%d", &p.IndustryType); err != nil {
		return DeviceIDParts{}, fmt.Errorf("industry type: %w", err)
	}
	if _, err := fmt.Sscanf(id[10:13], "%d", &p.DeviceType); err != nil {
		return DeviceIDParts{}, fmt.Errorf("device type: %w", err)
	}
	if _, err := fmt.Sscanf(id[13:20], "%d", &p.Serial); err != nil {
		return DeviceIDParts{}, fmt.Errorf("serial: %w", err)
	}
	return p, nil
}
