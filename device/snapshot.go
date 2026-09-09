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

// SnapshotExecutor is the host-side half of the GB/T 28181-2022
// image-snapshot command (A.2.1.24): capture cmd.SnapNum JPEG frames and
// POST each body to cmd.UploadURL **verbatim** (the URL already carries
// the session parameter — the receiving platform owns that contract),
// returning one ID per successfully uploaded file. An empty result
// reports the exchange as wholly/partially failed (A.2.5.7).
//
// Install via Server.SetSnapshotExecutor; without one the server keeps
// its historical control-reject behavior.
type SnapshotExecutor interface {
	Execute(ctx context.Context, cmd manscdp.SnapShotCmd) ([]string, error)
}

// SetSnapshotExecutor installs the snapshot executor (optional; call
// before Start). UDP transport only — over another transport the
// snapshot control is rejected with a warning.
func (s *Server) SetSnapshotExecutor(exec SnapshotExecutor) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snapshotExecutor = exec
}

func (s *Server) snapshotExec() SnapshotExecutor {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshotExecutor
}

// parseSnapshotControl decodes a DeviceControl(SnapShot) body; ok=false
// for anything else.
func parseSnapshotControl(body string) (manscdp.DeviceControl, bool) {
	ct, v, err := manscdp.Decode([]byte(body))
	if err != nil || ct != manscdp.CmdDeviceControl {
		return manscdp.DeviceControl{}, false
	}
	dc, ok := v.(manscdp.DeviceControl)
	if !ok || dc.SnapShot == nil {
		return manscdp.DeviceControl{}, false
	}
	return dc, true
}

// runSnapshotExchange executes one platform snapshot command to
// completion: the executor captures/uploads, then the
// UploadSnapShotFinished notify (A.2.5.7) goes to the platform with
// fresh routing headers, mirroring the keepalive path. Executor errors
// report a failed exchange (empty SnapShotList); the returned IDs pass
// through verbatim.
func (s *Server) runSnapshotExchange(ctx context.Context, dc manscdp.DeviceControl, exec SnapshotExecutor) {
	cmd := *dc.SnapShot

	fileIDs, err := exec.Execute(ctx, cmd)
	if err != nil {
		slog.Warn("gb28181: snapshot exchange failed", "error", err)
		fileIDs = nil
	}

	platformAddr, err := net.ResolveUDPAddr("udp",
		net.JoinHostPort(s.cfg.PlatformSIPAddress, strconv.Itoa(s.cfg.PlatformSIPPort)))
	if err != nil {
		slog.Error("gb28181: snapshot notify platform address", "error", err)
		return
	}

	localIPAddr, err := getLocalIP(ctx, platformAddr.String())
	if err != nil {
		slog.Warn("gb28181: failed to determine local IP, falling back to interface scan", "error", err)
		localIPAddr = localIP()
	}
	domain := s.cfg.SIPDomain
	notify := BuildUploadSnapShotFinishedMessage(dc.SN, s.cfg.DeviceID, cmd.SessionID, fileIDs)
	notify.RequestURI = fmt.Sprintf("sip:%s@%s", domain, domain)
	notify.From = fmt.Sprintf("<sip:%s@%s>", s.cfg.DeviceID, domain)
	notify.To = fmt.Sprintf("<sip:%s@%s>", domain, domain)
	notify.CallID = fmt.Sprintf("snapshot-%d@%s", time.Now().UnixNano(), localIPAddr)
	notify.CSeq = "1 MESSAGE"
	notify.Via = fmt.Sprintf("SIP/2.0/UDP %s:%d;branch=z9hG4bK%016x",
		localIPAddr, s.cfg.LocalSIPPort, time.Now().UnixNano())

	if err := s.sendToPlatform(notify, platformAddr); err != nil {
		slog.Error("gb28181: snapshot-finished notify send failed", "error", err)
	}
}
