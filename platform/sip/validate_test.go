package sip

import (
	"strings"
	"testing"
	"time"

	"github.com/mickeyzzc/gb28181-go/platform"
)

// Config.Validate (issue #41): the whole platform configuration is checked
// at Start — enum typos in YAML and impossible port pools must fail fast
// instead of surfacing as silent misbehavior.

func validPlatformConfig() Config {
	return Config{
		Enabled:   true,
		SIPListen: "0.0.0.0:5060",
		ServerID:  "34020000002000000001",
		Realm:     "3402000000",
		Password:  "secret",
		PortRange: "30000-30050",
	}
}

func TestPlatformConfigValidateAcceptsValid(t *testing.T) {
	if err := validPlatformConfig().Validate(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
}

func TestPlatformConfigValidateStubSkipsRequirements(t *testing.T) {
	// The loopback/conformance suites start servers without Enabled —
	// a stub must not fail structural validation either.
	if err := (Config{}).Validate(); err != nil {
		t.Fatalf("zero config rejected: %v", err)
	}
}

func TestPlatformConfigValidateEnums(t *testing.T) {
	cases := []struct {
		mutate  func(*Config)
		wantErr string
	}{
		{func(c *Config) { c.SIPTransport = "sctp" }, "SIPTransport"},
		{func(c *Config) { c.MediaTransport = "tcp-flaky" }, "MediaTransport"},
		{func(c *Config) { c.TCPFraming = "0x25" }, "TCPFraming"},
		{func(c *Config) { c.SubChannelProbe = "maybe" }, "SubChannelProbe"},
		{func(c *Config) { c.SubChannelProbeOffset = 100 }, "SubChannelProbeOffset"},
	}
	for _, tc := range cases {
		cfg := validPlatformConfig()
		tc.mutate(&cfg)
		err := cfg.Validate()
		if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
			t.Errorf("want %s error, got %v", tc.wantErr, err)
		}
	}
	// Valid enum spellings pass (empty = default). TLS transport belongs to
	// the TLS-combination test — it requires the cert pair.
	for _, cfg := range []Config{
		{},
		{SIPTransport: "udp"},
		{SIPTransport: "tcp"},
		{MediaTransport: "tcp-passive"},
		{MediaTransport: "udp"},
		{MediaTransport: "tcp-active"},
		{TCPFraming: "rfc4571"},
		{TCPFraming: "0x24"},
		{TCPFraming: "auto"},
		{SubChannelProbe: "auto"},
		{SubChannelProbe: "on"},
		{SubChannelProbe: "off"},
	} {
		if err := cfg.Validate(); err != nil {
			t.Errorf("%+v: unexpected error %v", cfg, err)
		}
	}
}

func TestPlatformConfigValidateDurations(t *testing.T) {
	for _, field := range []struct {
		name  string
		value string
	}{
		{"InviteResponseTimeout", "abc"},
		{"HeartbeatInterval", "-1s"},
		{"CatalogInterval", "0s"},
		{"SubscribeExpires", "not-a-duration"},
		{"RegisterFailureWindow", "1"},
		{"RegisterLockoutDuration", "xx"},
	} {
		cfg := validPlatformConfig()
		switch field.name {
		case "InviteResponseTimeout":
			cfg.InviteResponseTimeout = field.value
		case "HeartbeatInterval":
			cfg.HeartbeatInterval = field.value
		case "CatalogInterval":
			cfg.CatalogInterval = field.value
		case "SubscribeExpires":
			cfg.SubscribeExpires = field.value
		case "RegisterFailureWindow":
			cfg.RegisterFailureWindow = field.value
		case "RegisterLockoutDuration":
			cfg.RegisterLockoutDuration = field.value
		}
		err := cfg.Validate()
		if err == nil || !strings.Contains(err.Error(), field.name) {
			t.Errorf("%s=%q: want %s error, got %v", field.name, field.value, field.name, err)
		}
	}
}

func TestPlatformConfigValidateTLSCombination(t *testing.T) {
	cfg := validPlatformConfig()
	cfg.SIPTransport = "tls"
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "TLSCertFile") {
		t.Fatalf("tls without cert: want TLSCertFile error, got %v", err)
	}

	cfg.TLSCertFile, cfg.TLSKeyFile = "cert.pem", "key.pem"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("tls with cert pair: %v", err)
	}

	cfg2 := validPlatformConfig()
	cfg2.TLSCertFile = "cert.pem"
	if err := cfg2.Validate(); err == nil || !strings.Contains(err.Error(), "SIPTransport") {
		t.Fatalf("cert on udp transport: want SIPTransport error, got %v", err)
	}
}

func TestPlatformConfigValidateRequiredWhenEnabled(t *testing.T) {
	cases := []struct {
		mutate  func(*Config)
		wantErr string
	}{
		{func(c *Config) { c.SIPListen = "" }, "SIPListen"},
		{func(c *Config) { c.SIPListen = "not-a-listen" }, "SIPListen"},
		{func(c *Config) { c.ServerID = "1" }, "ServerID"},
		{func(c *Config) { c.Realm = "" }, "Realm"},
		{func(c *Config) { c.PortRange = "40000" }, "PortRange"},
		{func(c *Config) { c.PortRange = "40010-40000" }, "PortRange"},
		{func(c *Config) { c.PortRange = "70000-70010" }, "PortRange"},
	}
	for _, tc := range cases {
		cfg := validPlatformConfig()
		tc.mutate(&cfg)
		err := cfg.Validate()
		if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
			t.Errorf("want %s error, got %v", tc.wantErr, err)
		}
	}
}

func TestPlatformConfigValidateAllowedDeviceIDs(t *testing.T) {
	cfg := validPlatformConfig()
	cfg.AllowedDeviceIDs = []string{"34020000001320000001", ""}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "AllowedDeviceIDs") {
		t.Fatalf("want AllowedDeviceIDs error, got %v", err)
	}
}

func TestServerStartFailsFastOnInvalidConfig(t *testing.T) {
	cfg := validPlatformConfig()
	cfg.ServerID = "bogus"
	srv := NewServer(cfg, platform.NewDeviceManager(time.Minute),
		platform.NewSessionManager(platform.NewPortManager(30000, 30100), cfg.ServerID), nil)
	if err := srv.Start(t.Context()); err == nil {
		t.Fatal("Start must reject a malformed config (issue #41 fail-fast)")
	}
}
