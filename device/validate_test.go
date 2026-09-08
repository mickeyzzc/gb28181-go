package device

import (
	"strings"
	"testing"
)

// Config.Validate (issue #41): dangerous defaults must fail fast at Start
// instead of surfacing as a 0-second ticker or a broken dial address.

func validDeviceConfig() Config {
	return Config{
		Enabled:               true,
		PlatformSIPAddress:    "192.168.63.30",
		PlatformSIPPort:       5060,
		DeviceID:              "34020000001320000002",
		ChannelID:             "34020000001320000002",
		SIPDomain:             "3402000000",
		LocalSIPPort:          5060,
		RegisterIntervalSecs:  60,
		HeartbeatIntervalSecs: 60,
		HeartbeatTimeoutCount: 3,
	}
}

func TestDeviceConfigValidateAcceptsValid(t *testing.T) {
	if err := validDeviceConfig().Validate(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
}

func TestDeviceConfigValidateDisabledSkipsRequirements(t *testing.T) {
	// A disabled stub (empty address, zero intervals) must not fail —
	// hosts ship YAML stubs with enabled:false.
	cfg := Config{}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("disabled stub rejected: %v", err)
	}
}

func TestDeviceConfigValidateRequiresWhenEnabled(t *testing.T) {
	cases := []struct {
		mutate  func(*Config)
		wantErr string
	}{
		{func(c *Config) { c.PlatformSIPAddress = "" }, "PlatformSIPAddress"},
		{func(c *Config) { c.PlatformSIPPort = 0 }, "PlatformSIPPort"},
		{func(c *Config) { c.PlatformSIPPort = 70000 }, "PlatformSIPPort"},
		{func(c *Config) { c.DeviceID = "123" }, "DeviceID"},
		{func(c *Config) { c.ChannelID = "abc" }, "ChannelID"},
		{func(c *Config) { c.SIPDomain = "" }, "SIPDomain"},
		{func(c *Config) { c.RegisterIntervalSecs = 0 }, "RegisterIntervalSecs"},
		{func(c *Config) { c.RegisterIntervalSecs = -1 }, "RegisterIntervalSecs"},
		{func(c *Config) { c.HeartbeatIntervalSecs = 0 }, "HeartbeatIntervalSecs"},
		{func(c *Config) { c.HeartbeatTimeoutCount = 0 }, "HeartbeatTimeoutCount"},
	}
	for _, tc := range cases {
		cfg := validDeviceConfig()
		tc.mutate(&cfg)
		err := cfg.Validate()
		if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
			t.Errorf("want %s error, got %v", tc.wantErr, err)
		}
	}
}

func TestDeviceConfigValidateTransport(t *testing.T) {
	for _, tr := range []string{"", "udp", "tcp", "UDP"} {
		cfg := validDeviceConfig()
		cfg.Transport = tr
		if err := cfg.Validate(); err != nil {
			t.Errorf("transport %q: unexpected error %v", tr, err)
		}
	}
	cfg := validDeviceConfig()
	cfg.Transport = "carrier-pigeon"
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "Transport") {
		t.Errorf("bogus transport: want Transport error, got %v", err)
	}
}

func TestDeviceConfigValidateTLSCombination(t *testing.T) {
	// TLS transport needs a CA (or explicit insecure opt-in).
	cfg := validDeviceConfig()
	cfg.Transport = "tls"
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "TLSCAFile") {
		t.Fatalf("tls without CA: want TLSCAFile error, got %v", err)
	}

	cfg.TLSInsecureSkipVerify = true
	if err := cfg.Validate(); err != nil {
		t.Fatalf("tls with insecure opt-in: %v", err)
	}

	// TLS material on a non-TLS transport is a misconfiguration.
	cfg2 := validDeviceConfig()
	cfg2.TLSCAFile = "/etc/ca.pem"
	if err := cfg2.Validate(); err == nil || !strings.Contains(err.Error(), "Transport") {
		t.Fatalf("CA on udp transport: want Transport error, got %v", err)
	}
}

func TestDeviceConfigValidateLocalPortRange(t *testing.T) {
	cfg := validDeviceConfig()
	cfg.LocalSIPPort = 70000
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "LocalSIPPort") {
		t.Fatalf("want LocalSIPPort error, got %v", err)
	}
	// 0 is legitimate (kernel-assigned bind for tests).
	cfg.LocalSIPPort = 0
	if err := cfg.Validate(); err != nil {
		t.Fatalf("LocalSIPPort 0: %v", err)
	}
}
