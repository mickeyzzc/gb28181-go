package device

import (
	"fmt"
	"strings"
)

// Validate checks the Config and returns the first problem, or nil.
// Start calls it and fails fast (issue #41): a zero interval or a
// malformed address must refuse to start instead of surfacing as a
// 0-second ticker or a broken dial address.
//
// Only structural fields (transport enum, port ranges) are checked on a
// disabled stub — hosts ship `enabled: false` YAML sections with empty
// values. Everything the REGISTER lifecycle needs is required once
// Enabled is set.
func (c Config) Validate() error {
	transport := strings.ToLower(c.Transport)
	switch transport {
	case "", "udp", "tcp", "tls":
	default:
		return fmt.Errorf("device.Config.Transport %q must be udp, tcp, or tls", c.Transport)
	}
	if c.LocalSIPPort < 0 || c.LocalSIPPort > 65535 {
		return fmt.Errorf("device.Config.LocalSIPPort %d out of range 0-65535 (0 = kernel-assigned)", c.LocalSIPPort)
	}

	// TLS material pairs with Transport "tls" only; the reverse (tls
	// without any trust anchor) trusts nothing or everything by accident.
	hasTLSMaterial := c.TLSCAFile != "" || c.TLSCertFile != "" || c.TLSKeyFile != ""
	if transport != "tls" && hasTLSMaterial {
		return fmt.Errorf("device.Config TLS files require Transport %q, got %q", "tls", c.Transport)
	}

	if !c.Enabled {
		return nil
	}

	if c.PlatformSIPAddress == "" {
		return fmt.Errorf("device.Config.PlatformSIPAddress is required when enabled")
	}
	if c.PlatformSIPPort < 1 || c.PlatformSIPPort > 65535 {
		return fmt.Errorf("device.Config.PlatformSIPPort %d out of range 1-65535", c.PlatformSIPPort)
	}
	if err := validateGBID("DeviceID", c.DeviceID); err != nil {
		return err
	}
	if err := validateGBID("ChannelID", c.ChannelID); err != nil {
		return err
	}
	if c.SIPDomain == "" {
		return fmt.Errorf("device.Config.SIPDomain is required when enabled")
	}
	if c.RegisterIntervalSecs <= 0 {
		return fmt.Errorf("device.Config.RegisterIntervalSecs must be positive, got %d", c.RegisterIntervalSecs)
	}
	if c.HeartbeatIntervalSecs <= 0 {
		return fmt.Errorf("device.Config.HeartbeatIntervalSecs must be positive, got %d", c.HeartbeatIntervalSecs)
	}
	if c.HeartbeatTimeoutCount <= 0 {
		return fmt.Errorf("device.Config.HeartbeatTimeoutCount must be positive, got %d", c.HeartbeatTimeoutCount)
	}
	if transport == "tls" && c.TLSCAFile == "" && !c.TLSInsecureSkipVerify {
		return fmt.Errorf("device.Config.TLSCAFile is required for Transport tls (or set TLSInsecureSkipVerify explicitly)")
	}
	return nil
}

// validateGBID checks the GB/T 28181 20-digit identifier convention.
func validateGBID(field, id string) error {
	if len(id) != 20 {
		return fmt.Errorf("device.Config.%s %q must be 20 digits", field, id)
	}
	for _, r := range id {
		if r < '0' || r > '9' {
			return fmt.Errorf("device.Config.%s %q must be 20 digits", field, id)
		}
	}
	return nil
}
