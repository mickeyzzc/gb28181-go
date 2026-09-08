package sip

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

// Validate checks the Config and returns the first problem, or nil.
// Start calls it and fails fast (issue #41): enum typos and impossible
// port pools must refuse to start instead of surfacing as silent
// misbehavior.
//
// Only structural fields (enums, duration strings, port-pool shape) are
// checked on a stub; the identity fields (SIPListen, ServerID, Realm,
// PortRange semantics) are required once Enabled is set — the
// loopback/conformance suites deliberately start servers without Enabled.
func (c Config) Validate() error {
	switch strings.ToLower(c.SIPTransport) {
	case "", "udp", "tcp", "tls":
	default:
		return fmt.Errorf("sip.Config.SIPTransport %q must be udp, tcp, or tls", c.SIPTransport)
	}
	switch c.MediaTransport {
	case "", "tcp-passive", "udp", "tcp-active":
	default:
		return fmt.Errorf("sip.Config.MediaTransport %q must be tcp-passive, udp, or tcp-active", c.MediaTransport)
	}
	switch c.TCPFraming {
	case "", "rfc4571", "0x24", "auto":
	default:
		return fmt.Errorf("sip.Config.TCPFraming %q must be rfc4571, 0x24, or auto", c.TCPFraming)
	}
	switch c.SubChannelProbe {
	case "", "auto", "on", "off":
	default:
		return fmt.Errorf("sip.Config.SubChannelProbe %q must be auto, on, or off", c.SubChannelProbe)
	}
	if c.SubChannelProbeOffset < 0 || c.SubChannelProbeOffset > 99 {
		return fmt.Errorf("sip.Config.SubChannelProbeOffset %d out of range 0-99 (0 disables)", c.SubChannelProbeOffset)
	}

	for _, d := range []struct {
		name  string
		value string
	}{
		{"RegisterFailureWindow", c.RegisterFailureWindow},
		{"RegisterLockoutDuration", c.RegisterLockoutDuration},
		{"InviteResponseTimeout", c.InviteResponseTimeout},
		{"HeartbeatInterval", c.HeartbeatInterval},
		{"CatalogInterval", c.CatalogInterval},
		{"SubscribeExpires", c.SubscribeExpires},
	} {
		if d.value == "" {
			continue
		}
		parsed, err := time.ParseDuration(d.value)
		if err != nil || parsed <= 0 {
			return fmt.Errorf("sip.Config.%s %q must be a positive Go duration", d.name, d.value)
		}
	}

	hasTLSMaterial := c.TLSCertFile != "" || c.TLSKeyFile != ""
	if strings.ToLower(c.SIPTransport) == "tls" {
		if c.TLSCertFile == "" || c.TLSKeyFile == "" {
			return fmt.Errorf("sip.Config.TLSCertFile and TLSKeyFile are required for SIPTransport tls")
		}
	} else if hasTLSMaterial {
		return fmt.Errorf("sip.Config TLS files require SIPTransport %q", "tls")
	}

	for _, id := range c.AllowedDeviceIDs {
		if id == "" {
			return fmt.Errorf("sip.Config.AllowedDeviceIDs contains an empty entry")
		}
	}

	if c.PortRange != "" {
		if err := validatePortRange(c.PortRange); err != nil {
			return err
		}
	}

	if !c.Enabled {
		return nil
	}

	if c.SIPListen == "" {
		return fmt.Errorf("sip.Config.SIPListen is required when enabled")
	}
	host, _, err := parseSIPListen(c.SIPListen)
	if err != nil {
		return fmt.Errorf("sip.Config.SIPListen %q: %w", c.SIPListen, err)
	}
	if host != "" && net.ParseIP(host) == nil {
		return fmt.Errorf("sip.Config.SIPListen host %q must be an IP address", host)
	}
	if len(c.ServerID) != 20 || !allDigits(c.ServerID) {
		return fmt.Errorf("sip.Config.ServerID %q must be the 20-digit platform serial", c.ServerID)
	}
	if c.Realm == "" {
		return fmt.Errorf("sip.Config.Realm is required when enabled")
	}
	return nil
}

// validatePortRange checks the "start-end" RTP media pool shape.
func validatePortRange(v string) error {
	lo, hi, ok := strings.Cut(v, "-")
	if !ok {
		return fmt.Errorf("sip.Config.PortRange %q must be \"start-end\"", v)
	}
	loPort, err1 := strconv.Atoi(lo)
	hiPort, err2 := strconv.Atoi(hi)
	if err1 != nil || err2 != nil || loPort < 1 || hiPort > 65535 || loPort > hiPort {
		return fmt.Errorf("sip.Config.PortRange %q must be two ports start<=end in 1-65535", v)
	}
	return nil
}

func allDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
