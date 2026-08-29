package sip

import "time"

// Config configures the GB/T 28181 platform (UAS) server. Field-compatible
// with MiBeeNvr's config.GB28181ServerConfig — the host maps its own config
// onto this struct.
type Config struct {
	Enabled bool `yaml:"enabled"`

	// SIPListen is the SIP UDP/TCP listen address, e.g. ":5060".
	SIPListen string `yaml:"sip_listen"`

	// ServerID is the platform GB 20-digit serial (e.g. "34020000002000000001").
	ServerID string `yaml:"server_id"`

	// Realm is the SIP digest-auth realm presented in REGISTER challenges.
	Realm string `yaml:"realm"`

	// Password is the SIP digest-auth secret that registered devices must use.
	Password string `yaml:"password"`

	// PortRange is the RTP media port pool, "start-end". Default "30000-30050".
	PortRange string `yaml:"port_range"`

	// AllowedDeviceIDs restricts which devices may register. Empty = allow any.
	AllowedDeviceIDs []string `yaml:"allowed_device_ids,omitempty"`

	// AllowSameIPEnroll opts GB28181 auto-enroll out of the cross-protocol
	// dedup: when true, a channel whose device registers from an IP that
	// already hosts another camera's stream is STILL auto-enrolled as its own
	// camera. For deliberate dual-protocol setups. Default false.
	AllowSameIPEnroll bool `yaml:"allow_same_ip_enroll,omitempty"`

	// HeartbeatInterval is how often devices are expected to send Keepalive.
	HeartbeatInterval string `yaml:"heartbeat_interval"` // default "60s"

	// CatalogInterval is how often the platform refreshes the device catalog.
	CatalogInterval string `yaml:"catalog_interval"` // default "30m"

	// SubChannelProbe gates the sub-channel prober: "auto" (default),
	// "on", or "off".
	SubChannelProbe string `yaml:"sub_channel_probe,omitempty"`

	// SubChannelProbeOffset is the channel-code numeric offset the prober
	// applies to derive the sub-channel candidate (Hikvision convention: +1).
	// 0 disables probing regardless of SubChannelProbe. Range 1–99.
	SubChannelProbeOffset int `yaml:"sub_channel_probe_offset,omitempty"`

	// TCPMode forces TCP media transport (passive).
	//
	// Deprecated: superseded by MediaTransport; kept as a YAML-compat no-op.
	TCPMode bool `yaml:"tcp_mode"`

	// TCPFraming selects the TCP-passive framing: "rfc4571", "0x24", or "auto".
	TCPFraming string `yaml:"tcp_framing"` // default "auto"

	// MediaTransport selects the RTP media transport for INVITE sessions:
	// "tcp-passive" (default), "udp", or "tcp-active".
	MediaTransport string `yaml:"media_transport"`

	// SIPTransport selects the SIP signaling listener: "udp" (default),
	// "tcp" (adds a SIP-over-TCP listener alongside UDP), or "tls" (SIPS —
	// TLS listener per GB/T 28181-2022 A-level security; outbound requests
	// to devices then carry ;transport=tls and reuse the connection the
	// device registered over).
	SIPTransport string `yaml:"sip_transport"`

	// TLSCertFile / TLSKeyFile are the SIPS server certificate pair
	// (PEM). Required when SIPTransport is "tls".
	TLSCertFile string `yaml:"tls_cert_file,omitempty"`
	TLSKeyFile  string `yaml:"tls_key_file,omitempty"`

	// SubscribeCatalog enables SUBSCRIBE Catalog. Default: on when Enabled.
	SubscribeCatalog *bool `yaml:"subscribe_catalog,omitempty"`

	// SubscribeAlarm enables SUBSCRIBE Alarm. Default: on when Enabled.
	SubscribeAlarm *bool `yaml:"subscribe_alarm,omitempty"`

	// SubscribeMobilePosition enables SUBSCRIBE MobilePosition for moving
	// devices. Default false — stationary cameras never emit reports.
	SubscribeMobilePosition bool `yaml:"subscribe_mobile_position"`

	// SubscribeExpires is the SUBSCRIBE lifetime; renewed at 80%.
	SubscribeExpires string `yaml:"subscribe_expires"` // default "3600s"

	// AlarmLinkage configures alarm-triggered streaming: on an alarm
	// notification, INVITE the alarm channel when it is not already streaming,
	// hold the stream for the configured duration, then BYE.
	AlarmLinkage *AlarmLinkageConfig `yaml:"alarm_linkage,omitempty"`
}

// AlarmLinkageConfig is the alarm→stream linkage block.
type AlarmLinkageConfig struct {
	Enabled bool `yaml:"enabled"`

	// Duration of each alarm-triggered stream hold. Default "60s".
	Duration string `yaml:"duration"`
}

// AlarmLinkageDuration resolves the hold duration with its default.
func (c *AlarmLinkageConfig) AlarmLinkageDuration() time.Duration {
	if c == nil {
		return 0
	}
	if d, err := time.ParseDuration(c.Duration); err == nil && d > 0 {
		return d
	}
	return 60 * time.Second
}

// CatalogSubscriptionOn resolves the subscribe_catalog toggle: on when the
// server is enabled and the flag is unset or true.
func (c Config) CatalogSubscriptionOn() bool {
	if c.SubscribeCatalog == nil {
		return c.Enabled
	}
	return *c.SubscribeCatalog
}

// AlarmSubscriptionOn resolves the subscribe_alarm toggle.
func (c Config) AlarmSubscriptionOn() bool {
	if c.SubscribeAlarm == nil {
		return c.Enabled
	}
	return *c.SubscribeAlarm
}
