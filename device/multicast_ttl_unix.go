//go:build !windows

package device

import (
	"net"
	"syscall"
)

// setMulticastTTL sets IP_MULTICAST_TTL on the media socket when the
// INVITE's media address is a multicast group (LAN default is 1;
// multi-subnet deployments raise it).
func setMulticastTTL(conn *net.UDPConn, ttl int) error {
	rc, err := conn.SyscallConn()
	if err != nil {
		return err
	}
	var sockErr error
	if err := rc.Control(func(fd uintptr) {
		sockErr = syscall.SetsockoptInt(int(fd), syscall.IPPROTO_IP, syscall.IP_MULTICAST_TTL, ttl)
	}); err != nil {
		return err
	}
	return sockErr
}
