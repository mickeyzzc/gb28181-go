//go:build gb35114

package sip

import (
	"context"
	"net"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/emmansun/gmsm/smx509"
	"github.com/mickeyzzc/gb28181-go/device"
	"github.com/mickeyzzc/gb28181-go/platform"
	sec "github.com/mickeyzzc/gb28181-go/security35114"
)

// fixtureDir points at the committed SM2 test identities.
var fixtureDir = filepath.Join("..", "..", "security35114", "testdata")

// countingPlatform delegates to the real security35114.Platform and counts
// Note verifications so the loopback test can observe the integrity path.
type countingPlatform struct {
	*sec.Platform
	notes    atomic.Int32
	noteErrs atomic.Int32
}

func (c *countingPlatform) VerifyNote(deviceID, note, method, from, to, callID, date, body string) error {
	err := c.Platform.VerifyNote(deviceID, note, method, from, to, callID, date, body)
	if err != nil {
		c.noteErrs.Add(1)
	} else {
		c.notes.Add(1)
	}
	return err
}

// TestGB35114Loopback drives a REAL device.Server against this package's
// Server across loopback UDP: Capability REGISTER → Bidirection challenge →
// signed re-REGISTER (real SM2 sign1) → 200 OK SecurityInfo (sealed VKEK +
// real sign2) → keepalive MESSAGEs whose Note headers verify under the
// negotiated VKEK.
func TestGB35114Loopback(t *testing.T) {
	devIdentity, err := sec.LoadIdentityFromFiles(
		filepath.Join(fixtureDir, "device_cert.pem"),
		filepath.Join(fixtureDir, "device_key.pem"),
	)
	if err != nil {
		t.Fatalf("device identity: %v", err)
	}
	platIdentity, err := sec.LoadIdentityFromFiles(
		filepath.Join(fixtureDir, "platform_cert.pem"),
		filepath.Join(fixtureDir, "platform_key.pem"),
	)
	if err != nil {
		t.Fatalf("platform identity: %v", err)
	}

	cfg := testConfig(t)
	cfg.Password = "" // A-level replaces digest for this device
	platform35114, err := sec.NewPlatform(sec.PlatformConfig{
		ServerID:    testServerID,
		Identity:    platIdentity,
		DeviceCerts: map[string]*smx509.Certificate{testDeviceID: devIdentity.Certificate},
	})
	if err != nil {
		t.Fatalf("NewPlatform: %v", err)
	}
	counting := &countingPlatform{Platform: platform35114}
	cfg.RegisterAuthenticator = counting
	_, dm := startTestServer(t, cfg)

	deviceAuth, err := sec.New(sec.Options{
		Device:       devIdentity,
		PlatformCert: platIdentity.Certificate,
		DeviceID:     testDeviceID,
		ServerID:     testServerID,
		KeyVersion:   "2026-01-01T00:00:00.000",
	})
	if err != nil {
		t.Fatalf("device authenticator: %v", err)
	}

	host, portStr, err := net.SplitHostPort(cfg.SIPListen)
	if err != nil {
		t.Fatalf("splitting listen %q: %v", cfg.SIPListen, err)
	}
	platformPort, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("parsing port %q: %v", portStr, err)
	}
	devCfg := device.Config{
		Enabled:               true,
		PlatformSIPAddress:    host,
		PlatformSIPPort:       platformPort,
		DeviceID:              testDeviceID,
		ChannelID:             "34020000001310000001",
		SIPDomain:             testServerID,
		Password:              "unused-with-authenticator",
		LocalSIPPort:          freeUDPPort(t),
		RegisterIntervalSecs:  2,
		HeartbeatIntervalSecs: 1,
		HeartbeatTimeoutCount: 3,
		Transport:             "udp",
		RegisterAuthenticator: deviceAuth,
	}
	devSrv := device.New(devCfg, device.DeviceInfo{Name: "loopback"}, device.NewFrameHub())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- devSrv.Start(ctx) }()

	// The handshake lands once the device's first REGISTER cycle completes.
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := dm.Device(testDeviceID); ok && platform35114.VKEK(testDeviceID) != nil {
			break
		}
		select {
		case err := <-errCh:
			t.Fatalf("device server exited: %v", err)
		case <-time.After(100 * time.Millisecond):
		}
	}
	if platform35114.VKEK(testDeviceID) == nil {
		t.Fatalf("handshake did not complete: platform has no VKEK for %s", testDeviceID)
	}
	if vkek := deviceAuth.VKEK(); vkek == nil {
		t.Fatalf("device did not accept the SecurityInfo")
	}
	if string(platform35114.VKEK(testDeviceID)) != string(deviceAuth.VKEK()) {
		t.Fatalf("negotiated VKEKs differ between device and platform")
	}

	// Keepalives (1s cadence) must carry Notes that verify under the VKEK.
	for time.Now().Before(deadline) && counting.notes.Load() < 2 {
		select {
		case err := <-errCh:
			t.Fatalf("device server exited: %v", err)
		case <-time.After(100 * time.Millisecond):
		}
	}
	if got := counting.notes.Load(); got < 2 {
		t.Fatalf("verified Notes = %d (errors = %d), want ≥2 verified keepalives", got, counting.noteErrs.Load())
	}
	if dev, ok := dm.Device(testDeviceID); !ok || dev.Status.Load() != platform.DeviceOnline {
		t.Fatalf("device not online after keepalives")
	}
}
