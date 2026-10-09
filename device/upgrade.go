package device

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"time"

	"github.com/mickeyzzc/gb28181-go/manscdp"
)

// DeviceUpgradeOutcome reports one A.2.3.1.12 upgrade attempt: the
// firmware in effect after the attempt (required by the A.2.5.9 notify)
// and, on failure, the reason — 01 download timeout, 02 package corrupt,
// 03 system error, 99 other.
type DeviceUpgradeOutcome struct {
	Success bool
	// Firmware in effect after the attempt (the new version on success,
	// the unchanged one on failure).
	Firmware string
	// FailedReason is the A.2.5.9 UpgradeFailedReason ("01"/"02"/"03"/"99");
	// ignored on success.
	FailedReason string
}

// DeviceUpgrader is the host-side half of the GB/T 28181-2022
// firmware-upgrade command (A.2.3.1.12): download cmd.FileURL, apply the
// firmware, and report the outcome. The library correlates the A.2.5.9
// DeviceUpgradeResult notify with the request's SessionID — the actual
// fetch and flash belong to the host (the standard deliberately leaves
// the transfer to FileURL's scheme).
//
// Install via Server.SetDeviceUpgrader; without one the server keeps its
// explicit control reject.
type DeviceUpgrader interface {
	Upgrade(ctx context.Context, cmd manscdp.DeviceUpgradeCmd) (DeviceUpgradeOutcome, error)
}

// SetDeviceUpgrader installs the upgrade executor (optional; call before
// Start). UDP transport only — over another transport the upgrade control
// is rejected with a warning.
func (s *Server) SetDeviceUpgrader(up DeviceUpgrader) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deviceUpgrader = up
}

func (s *Server) upgrader() DeviceUpgrader {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.deviceUpgrader
}

// parseUpgradeControl decodes a DeviceControl(DeviceUpgrade) body;
// ok=false for anything else.
func parseUpgradeControl(body string) (manscdp.DeviceControl, bool) {
	ct, v, err := manscdp.Decode([]byte(body))
	if err != nil || ct != manscdp.CmdDeviceControl {
		return manscdp.DeviceControl{}, false
	}
	dc, ok := v.(manscdp.DeviceControl)
	if !ok || dc.DeviceUpgrade == nil {
		return manscdp.DeviceControl{}, false
	}
	return dc, true
}

// runUpgradeExchange executes one upgrade command to completion: the
// upgrader downloads/applies, then the A.2.5.9 DeviceUpgradeResult notify
// goes to the platform with fresh routing headers, mirroring the
// snapshot exchange. Upgrader errors report the 99 (other) failure.
func (s *Server) runUpgradeExchange(ctx context.Context, dc manscdp.DeviceControl, up DeviceUpgrader) {
	cmd := *dc.DeviceUpgrade

	outcome, err := up.Upgrade(ctx, cmd)
	if err != nil {
		slog.Warn("gb28181: device upgrade failed", "error", err)
		outcome = DeviceUpgradeOutcome{Success: false, Firmware: outcome.Firmware, FailedReason: "99"}
	}

	platformAddr, err := net.ResolveUDPAddr("udp",
		net.JoinHostPort(s.cfg.PlatformSIPAddress, strconv.Itoa(s.cfg.PlatformSIPPort)))
	if err != nil {
		slog.Error("gb28181: upgrade notify platform address", "error", err)
		return
	}

	localIPAddr, err := getLocalIP(ctx, platformAddr.String())
	if err != nil {
		slog.Warn("gb28181: failed to determine local IP, falling back to interface scan", "error", err)
		localIPAddr = localIP()
	}
	domain := s.cfg.SIPDomain
	notify := BuildDeviceUpgradeResultMessage(dc.SN, s.cfg.DeviceID, cmd.SessionID,
		outcome.Success, outcome.Firmware, outcome.FailedReason)
	notify.RequestURI = fmt.Sprintf("sip:%s@%s", domain, domain)
	notify.From = fmt.Sprintf("<sip:%s@%s>", s.cfg.DeviceID, domain)
	notify.To = fmt.Sprintf("<sip:%s@%s>", domain, domain)
	notify.CallID = fmt.Sprintf("upgrade-%d@%s", time.Now().UnixNano(), localIPAddr)
	notify.CSeq = "1 MESSAGE"
	notify.Via = fmt.Sprintf("SIP/2.0/UDP %s:%d;branch=z9hG4bK%016x",
		localIPAddr, s.cfg.LocalSIPPort, time.Now().UnixNano())

	if err := s.sendToPlatform(notify, platformAddr); err != nil {
		slog.Error("gb28181: upgrade-result notify send failed", "error", err)
	}
}
