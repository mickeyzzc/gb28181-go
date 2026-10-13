//go:build windows

package device

import "net"

// Windows: the multicast TTL knob is a no-op (documented; the OS
// default applies).
func setMulticastTTL(*net.UDPConn, int) error { return nil }
