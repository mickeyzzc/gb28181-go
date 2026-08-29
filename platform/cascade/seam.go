package cascade

import (
	"context"
	"time"
)

// Config configures the cascade client (this platform registering as a
// lower-level platform to an upper platform). Field-compatible with
// MiBeeNvr's config.GB28181CascadeConfig.
type Config struct {
	Enabled bool `yaml:"enabled"`

	// ServerDomain is the upper platform's 20-digit GB ID.
	ServerDomain string `yaml:"server_domain"`

	// ServerAddr is the upper platform's SIP address, "host:port".
	ServerAddr string `yaml:"server_addr"`

	// LocalDeviceID is THIS platform's 20-digit GB device ID as seen by the
	// upper platform.
	LocalDeviceID string `yaml:"local_device_id"`

	// Realm is the digest realm the upper platform challenges with.
	Realm string `yaml:"realm"`

	// Password is the SIP digest secret.
	Password string `yaml:"password"`

	// SIPListen is the cascade client's own SIP UDP port. Default ":5061".
	SIPListen string `yaml:"sip_listen"`

	// HeartbeatInterval is the Keepalive MESSAGE cadence. Default "60s".
	HeartbeatInterval string `yaml:"heartbeat_interval"`

	// RegisterExpires is the REGISTER lifetime in seconds (re-registered at
	// 80%). Default 3600.
	RegisterExpires int `yaml:"register_expires"`

	// Upstreams appends additional upper platforms beyond the legacy single
	// form (ServerAddr non-empty becomes uppers[0]).
	Upstreams []Upstream `yaml:"upstreams,omitempty"`

	// DeviceName / Manufacturer / Model identify this platform in DeviceInfo
	// answers toward the upper platform. Zero values fall back to the
	// MiBee NVR identity of the source implementation.
	DeviceName   string `yaml:"device_name,omitempty"`
	Manufacturer string `yaml:"manufacturer,omitempty"`
	Model        string `yaml:"model,omitempty"`
}

// Upstream is one upper-platform registration: its own REGISTER/keepalive
// loop and online state over the shared SIP listener.
type Upstream struct {
	ServerDomain      string `yaml:"server_domain"`
	ServerAddr        string `yaml:"server_addr"`
	LocalDeviceID     string `yaml:"local_device_id,omitempty"`
	Realm             string `yaml:"realm,omitempty"`
	Password          string `yaml:"password,omitempty"`
	HeartbeatInterval string `yaml:"heartbeat_interval,omitempty"`
	RegisterExpires   int    `yaml:"register_expires,omitempty"`
}

// CascadeChannel is the persisted channel-ID assignment: the first-seen GB
// channel ID allocated for a local camera and kept stable across restarts.
type CascadeChannel struct {
	CameraID    string
	GBChannelID string
	Name        string
	UpdatedAt   time.Time
}

// Recording is the host recording index entry subset the cascade needs.
// FilePath points at a recorded segment file the SegmentParser can read.
type Recording struct {
	ID        string
	CameraID  string
	FilePath  string
	Format    Format
	StartedAt time.Time
	EndedAt   time.Time
	Duration  float64 // seconds; informational
}

// Format values for Recording.Format.
const (
	FormatH264 Format = "h264"
	FormatH265 Format = "h265"
)

// Format identifies a recording's video codec.
type Format string

// RecordingFilter selects recordings for a RecordInfo answer.
type RecordingFilter struct {
	CameraID  string
	StartTime time.Time
	EndTime   time.Time
	Limit     int
	SortBy    string
	SortOrder string
}

// Store persists cascade channel assignments and indexes recordings. The
// host implements it (MiBeeNvr adapts its storage.DB); nil disables
// RecordInfo answers and channel-ID stability (IDs are re-derived per boot).
type Store interface {
	UpsertCascadeChannel(ctx context.Context, ch CascadeChannel) error
	ListCascadeChannels(ctx context.Context) ([]CascadeChannel, error)
	ListRecordings(ctx context.Context, filter RecordingFilter) ([]Recording, error)
}

// SegmentInfo describes one recorded segment file at the granularity the
// cascade playback pump consumes. Hosts with an fMP4 recording pipeline
// (MiBeeNvr internal/merge) adapt their parser to this shape — the field
// names match so the adapter is usually a type-for-type wrapper.
type SegmentInfo struct {
	Codec     string // "h264" or "h265"
	SPS       []byte // H.264 only
	PPS       []byte // H.264 only
	VPS       []byte // H.265 only
	Timescale uint32
	Samples   []SegmentSample
}

// SegmentSample is one access unit within a segment file.
type SegmentSample struct {
	Offset     int64  // absolute file offset of the sample data
	Size       uint32 // size of the sample data
	Duration   uint32 // in timescale units
	IsKeyFrame bool
}

// SegmentParser reads one recorded segment file. Injected by the host;
// nil disables cascade playback (RecordInfo still answers if a Store is set).
type SegmentParser func(filePath string) (*SegmentInfo, error)
