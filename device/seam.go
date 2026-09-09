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

// GB35114NotePolicy selects the device-side behavior when a
// platform→device request fails incoming Note verification (issue #52).
type GB35114NotePolicy int

const (
	// GB35114NoteReject answers 403 Forbidden. The zero value: inside an
	// authenticated A-level session a Note that does not verify is
	// tampering and must fail closed. Note-less requests still pass
	// (mixed-mode Digest platforms).
	GB35114NoteReject GB35114NotePolicy = iota
	// GB35114NoteWarn only logs the failure and serves the request — for
	// rollout observation on deployments with clock skew or signature
	// quirks.
	GB35114NoteWarn
	// GB35114NoteOff disables incoming Note verification entirely.
	GB35114NoteOff
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
	Transport             string `yaml:"transport"`               // SIP transport: udp (default), tcp, or tls (SIPS, GB/T 28181-2022 A-level)

	// IncomingNotePolicy sets the failure behavior of device-side Note
	// verification for platform→device requests (issue #52). Only active
	// when RegisterAuthenticator implements IncomingNoteVerifier (the
	// GB35114 A-level reference implementation does). Default: reject.
	IncomingNotePolicy GB35114NotePolicy `yaml:"incoming_note_policy"`

	// MaxSIPMessageSize bounds one Content-Length framed message (headers +
	// body) on the TCP/TLS read path — a forged Content-Length header must
	// drop the connection, not allocate (issue #37). 0 = default 1 MiB;
	// >0 = custom limit; <0 = unlimited (tests only).
	MaxSIPMessageSize int `yaml:"max_sip_message_size,omitempty"`

	// TLS fields (Transport "tls" only — SIPS signaling per GB/T 28181-2022
	// A-level security). The GB convention is a self-signed CA whose serial
	// is the device/platform ID; the host provisions the files.
	TLSCAFile             string `yaml:"tls_ca_file,omitempty"`              // CA (or the platform's self-signed cert) used to verify the platform
	TLSCertFile           string `yaml:"tls_cert_file,omitempty"`            // optional client certificate (mutual auth)
	TLSKeyFile            string `yaml:"tls_key_file,omitempty"`             // client certificate key
	TLSInsecureSkipVerify bool   `yaml:"tls_insecure_skip_verify,omitempty"` // accept any platform cert (lab only)

	// RegisterAuthenticator opts into an alternative REGISTER
	// authentication strategy (e.g. GB 35114 A-level via the tagged
	// security35114 package). Nil keeps the built-in SIP Digest flow.
	// Not YAML-serializable: the host wires the implementation in code.
	RegisterAuthenticator RegisterAuthenticator `yaml:"-"`
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

// UserAgent is the SIP User-Agent stamped on every message the device
// role emits. The neutral default carries no host brand — hosts rebrand
// by assigning their own value before calling New/Start (concurrent
// mutation afterwards races the message builders).
var UserAgent = "GB28181-Go/1.0"

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
