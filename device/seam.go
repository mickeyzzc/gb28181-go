// Package device implements the GB/T 28181-2016/2022 device role (UAC):
// SIP registration with a platform, MANSCDP XML messaging, RTP/PS media
// push for live streaming, playback, and download.
//
// Extracted verbatim from the production implementation in
// mibee-eye-raspi-go (Mi-Bee Studio), hardened against real GB28181
// platforms (digest qop=auth, unique Via branches, local-IP detection,
// MANSCDP attribute form, SIP-over-TCP).
//
// # Host integration seams
//
// The package is capture- and storage-agnostic. The host injects:
//
//   - [FrameSource] — live video access units (bridge your capture hub)
//   - [RecordingSource] — recorded-segment index for RecordInfo/playback
//   - [Config] / [DeviceInfo] — connection settings and device identity
//
// Segment files use the reference format read by [OpenSegment]: bare
// Annex-B H.264 with a per-frame `<segment>.ts.jsonl` sidecar of
// millisecond timestamps.
package device

import (
	"context"
	"time"
)

// Config holds GB28181 device (UAC) connection settings. YAML shapes are
// identical to the source project's `gb28181:` section.
type Config struct {
	Enabled               bool   `yaml:"enabled"`                 // Enable GB28181 registration (default: false)
	PlatformSIPAddress    string `yaml:"platform_sip_address"`    // SIP server (platform) address
	PlatformSIPPort       int    `yaml:"platform_sip_port"`       // SIP server (platform) port
	DeviceID              string `yaml:"device_id"`               // GB28181 device ID (20 digits)
	ChannelID             string `yaml:"channel_id"`              // GB28181 channel ID (20 digits)
	SIPDomain             string `yaml:"sip_domain"`              // GB28181 SIP domain
	Password              string `yaml:"password"`                // SIP authentication password
	LocalSIPPort          int    `yaml:"local_sip_port"`          // Local SIP listening port
	RegisterIntervalSecs  int    `yaml:"register_interval_secs"`  // SIP REGISTER interval (seconds)
	HeartbeatIntervalSecs int    `yaml:"heartbeat_interval_secs"` // SIP keepalive heartbeat interval (seconds)
	HeartbeatTimeoutCount int    `yaml:"heartbeat_timeout_count"` // Missed heartbeats before declaring timeout
	Transport             string `yaml:"transport"`               // SIP transport: udp (default) or tcp
}

// DeviceInfo identifies the device in Catalog/DeviceInfo responses.
// YAML shapes are identical to the source project's `device:` section.
type DeviceInfo struct {
	Name         string `yaml:"name"`          // Camera friendly name
	Manufacturer string `yaml:"manufacturer"`  // Device manufacturer
	Model        string `yaml:"model"`         // Device model
	Firmware     string `yaml:"firmware"`      // Firmware version
	HardwareID   string `yaml:"hardware_id"`   // Device hardware identifier
	SerialNumber string `yaml:"serial_number"` // Device serial number
}

// NALU is one H.264 NAL unit (payload without Annex-B start code).
type NALU struct {
	Type  byte   // NALU type (first byte & 0x1F)
	Data  []byte // Raw NALU data (without start code)
	IsIDR bool   // True if type == 5
	IsSPS bool   // True if type == 7
	IsPPS bool   // True if type == 8
}

// AccessUnit is a complete H.264 access unit (one encoder frame).
type AccessUnit struct {
	NALUs     []NALU
	Timestamp time.Time
	KeyFrame  bool // True if contains IDR
}

// FrameSubscription is a live-frame subscription handed out by
// [FrameSource.Subscribe]. Cancel the context passed to Subscribe to tear
// the subscription down; [FrameSource.Unsubscribe] also works by ID.
type FrameSubscription struct {
	ID      string
	Channel <-chan AccessUnit
}

// FrameSource supplies live H.264 access units for INVITE-driven media
// sessions. Implement over your capture pipeline's fan-out hub; bounded
// channels with drop-on-full are expected (never block the producer).
type FrameSource interface {
	Subscribe(ctx context.Context) *FrameSubscription
	Unsubscribe(id string)
}

// SegmentMeta describes one recorded segment for RecordInfo queries and
// playback/download INVITEs.
type SegmentMeta struct {
	File      string `json:"file"`      // path relative to the recording root
	StartMS   int64  `json:"start_ms"`  // unix milliseconds of first frame
	EndMS     int64  `json:"end_ms"`    // unix milliseconds of last frame
	Size      int64  `json:"size"`      // segment file size in bytes
	Frames    int    `json:"frames"`    // number of access units
	Keyframes int    `json:"keyframes"` // number of keyframe access units
}
