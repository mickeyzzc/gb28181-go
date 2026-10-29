//go:build gb35114

// Reproducer: GB35114 level-C media fails closed once the device
// re-registers against a long-lived platform. Generation 1 (fresh platform)
// delivers signed+encrypted AUs clean; a later generation (new device
// instance, same identity, re-register) arms the decryptor with a stale VKEK
// generation and every access unit dies to "AU signature verification
// failed" — zero video, session BYE'd by no-frame watchdogs, forever.
// Level B (signatures without the VEK envelope) stays clean across
// generations — the defect is specific to the C-level key path.
package conformance

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/emmansun/gmsm/smx509"
	"github.com/mickeyzzc/gb28181-go/device"
	"github.com/mickeyzzc/gb28181-go/platform"
	gbsip "github.com/mickeyzzc/gb28181-go/platform/sip"
	"github.com/mickeyzzc/gb28181-go/security35114"
)

var (
	rereDomain    = "3402000000"
	rereServerID  = "34020000002000000001"
	rereDeviceID  = "34020000001320000001"
	rereChannelID = "34020000001310000001"
)

func freeUDPPortT(t *testing.T) int {
	t.Helper()
	c, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	return c.LocalAddr().(*net.UDPAddr).Port
}

// realNals returns frame-size NALs — real libx264 bytes when real_nals.json
// is present, else zero-filled bytes of the same sizes.
func realNals(t *testing.T) (sps, pps, idr, pic []byte) {
	sps, pps, idr, pic = make([]byte, 24), make([]byte, 5), make([]byte, 5446), make([]byte, 2399)
	sps[0], pps[0], idr[0], pic[0] = 0x67, 0x68, 0x65, 0x41
	if raw, err := os.ReadFile("real_nals.json"); err == nil {
		m := map[string][]byte{}
		if json.Unmarshal(raw, &m) == nil {
			return m["sps"], m["pps"], m["idr"], m["p"]
		}
	}
	return
}

// runGeneration brings up one device "process" (fresh device.Server + FrameHub
// + authenticator), registers it against the shared platform, INVITEs and
// streams real-size frames for a few seconds, then reports how many AUs were
// delivered and how many signatures were rejected.
func runGeneration(t *testing.T, psrv *gbsip.Server, dm *platform.DeviceManager, sm *platform.SessionManager, sipPort int, platIdentity *security35114.Identity, devIdentity *security35114.Identity, gen int) (deliveredCount, failures int) {
	t.Helper()
	ctx := context.Background()

	deviceAuth, err := security35114.New(security35114.Options{
		Device:       devIdentity,
		PlatformCert: platIdentity.Certificate,
		DeviceID:     rereDeviceID,
		ServerID:     rereServerID,
	})
	if err != nil {
		t.Fatal(err)
	}

	fh := device.NewFrameHub()
	dcfg := device.Config{
		Enabled:               true,
		PlatformSIPAddress:    "127.0.0.1",
		PlatformSIPPort:       sipPort,
		DeviceID:              rereDeviceID,
		ChannelID:             rereChannelID,
		SIPDomain:             rereDomain,
		LocalSIPPort:          freeUDPPortT(t),
		RegisterIntervalSecs:  3600,
		HeartbeatIntervalSecs: 60,
		HeartbeatTimeoutCount: 3,
		Transport:             "udp",
		RegisterAuthenticator: deviceAuth,
	}
	dsrv := device.New(dcfg, device.DeviceInfo{Name: fmt.Sprintf("repro-gen%d", gen)}, fh)
	started := make(chan error, 1)
	go func() { started <- dsrv.Start(ctx) }()
	t.Cleanup(func() {
		dsrv.Stop()
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Error("device server did not stop")
		}
	})

	deadline := time.Now().Add(10 * time.Second)
	channelUp := false
	for time.Now().Before(deadline) {
		if d, ok := dm.Device(rereDeviceID); ok && d.Status.Load() == platform.DeviceOnline {
			for _, ch := range dm.Channels(rereDeviceID) {
				if ch.ID == rereChannelID {
					channelUp = true
				}
			}
			if channelUp {
				break
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !channelUp {
		t.Fatal("device channel never appeared in the catalog")
	}
	if err := psrv.InviteChannel(rereDeviceID, rereChannelID); err != nil {
		t.Fatal(err)
	}

	sps, pps, idr, pic := realNals(t)
	go func() {
		for i := 0; ; i++ {
			au := device.AccessUnit{Timestamp: time.Now(), KeyFrame: i%4 == 0}
			if au.KeyFrame {
				au.NALUs = []device.NALU{{Type: 7, IsSPS: true, Data: sps}, {Type: 8, IsPPS: true, Data: pps}, {Type: 5, IsIDR: true, Data: idr}}
			} else {
				au.NALUs = []device.NALU{{Type: 1, Data: pic}}
			}
			fh.Write(au)
			time.Sleep(250 * time.Millisecond)
		}
	}()

	var delivered atomic.Int32
	var hubDone atomic.Bool
	deadline = time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if rc := sm.GetReceiver(rereChannelID); rc != nil {
			if h := sm.GetHub(rereChannelID); h != nil && !hubDone.Load() {
				hubDone.Store(true)
				_ = h.Subscribe("repro", func(pts int64, au [][]byte, isIDR bool) { delivered.Add(1) })
			}
			_, ver := rc.MediaSecurityArmed()
			f := rc.AUVerifyFailures()
			t.Logf("gen%d: armed verify=%v failures=%d delivered=%d", gen, ver, f, delivered.Load())
			if f > 0 {
				time.Sleep(2 * time.Second)
				return int(delivered.Load()), int(rc.AUVerifyFailures())
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return int(delivered.Load()), 0
}

// TestReRegister_LevelC_FailsClosed runs two device generations against one
// platform at level C: generation 1 must deliver signed+encrypted AUs clean;
// generation 2 (the cross-restart re-registration) is where the bug fires.
// The level comes from GB_LEVEL (B or C; default C) so one body covers both.
func TestReRegister_LevelC_FailsClosed(t *testing.T) {
	level := security35114.LevelC
	switch os.Getenv("GB_LEVEL") {
	case "B", "b":
		level = security35114.LevelB
	}
	ctx := context.Background()
	mediaBase := 24000 + 100*freeUDPPortT(t)%500
	sipPort := freeUDPPortT(t)

	devIdentity, err := security35114.LoadIdentityFromFiles(filepath.Join(gb35114FixtureDir, "device_cert.pem"), filepath.Join(gb35114FixtureDir, "device_key.pem"))
	if err != nil {
		t.Fatal(err)
	}
	platIdentity, err := security35114.LoadIdentityFromFiles(filepath.Join(gb35114FixtureDir, "platform_cert.pem"), filepath.Join(gb35114FixtureDir, "platform_key.pem"))
	if err != nil {
		t.Fatal(err)
	}

	plat35114, err := security35114.NewPlatform(security35114.PlatformConfig{
		ServerID:    rereServerID,
		Identity:    platIdentity,
		DeviceCerts: map[string]*smx509.Certificate{rereDeviceID: devIdentity.Certificate},
		Level:       level,
	})
	if err != nil {
		t.Fatal(err)
	}

	cfg := gbsip.Config{
		SIPListen:             fmt.Sprintf("127.0.0.1:%d", sipPort),
		ServerID:              rereServerID,
		Realm:                 rereDomain,
		PortRange:             fmt.Sprintf("%d-%d", mediaBase, mediaBase+99),
		MediaTransport:        platform.MediaTCPPassive,
		RegisterAuthenticator: plat35114,
	}
	dm := platform.NewDeviceManager(60 * time.Second)
	sm := platform.NewSessionManager(platform.NewPortManager(uint16(mediaBase), uint16(mediaBase+99)), cfg.ServerID)
	psrv := gbsip.NewServer(cfg, dm, sm, nil)
	if err := psrv.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = psrv.Stop() })

	d1, f1 := runGeneration(t, psrv, dm, sm, sipPort, platIdentity, devIdentity, 1)
	t.Logf("generation 1: delivered=%d failures=%d", d1, f1)
	if f1 > 0 {
		t.Fatalf("generation 1 must be clean (fresh platform): got %d failures — different bug than expected", f1)
	}

	// BYE the first session so generation 2 gets a fresh INVITE.
	if err := psrv.ByeChannel(rereDeviceID, rereChannelID); err != nil {
		t.Logf("BYE before gen2: %v (continuing)", err)
	}
	time.Sleep(500 * time.Millisecond)

	for gen := 2; gen <= 4; gen++ {
		if err := psrv.ByeChannel(rereDeviceID, rereChannelID); err != nil {
			t.Logf("BYE before gen%d: %v (continuing)", gen, err)
		}
		time.Sleep(500 * time.Millisecond)
		d, f := runGeneration(t, psrv, dm, sm, sipPort, platIdentity, devIdentity, gen)
		t.Logf("generation %d: delivered=%d failures=%d", gen, d, f)
		if f > 3 {
			t.Fatalf("BUG REPRODUCED at generation %d: level-%s media fails closed after re-registration (gen1 was clean)", gen, level)
		}
	}
	t.Logf("generations 2-4 clean at level %s — bug NOT reproduced in-library", level)
}
