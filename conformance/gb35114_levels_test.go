//go:build gb35114

package conformance

// GB 35114 level B/C end-to-end loopbacks: a real device.Server (security35114
// Authenticator) registers against a real platform/sip.Server (security35114
// Platform at level B or C), the platform INVITEs with an a=security offer,
// the device answers with the negotiated attribute (level C adds the sealed
// VEK), and real PS/RTP media flows — signed per AU at B, additionally
// SM4-OFB encrypted at C — arriving at the platform's frame hub byte-exact.
//
// The signaling path doubles as the downstream-integrity proof: every
// platform→device request (catalog MESSAGE, INVITE, BYE) carries the keyed
// Date+Note pair, and the device's default IncomingNotePolicy (Reject) fails
// the whole test if any of them does not verify.

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/emmansun/gmsm/smx509"
	"github.com/mickeyzzc/gb28181-go/device"
	"github.com/mickeyzzc/gb28181-go/platform"
	gbsip "github.com/mickeyzzc/gb28181-go/platform/sip"
	"github.com/mickeyzzc/gb28181-go/security35114"
	"github.com/stretchr/testify/require"
)

var gb35114FixtureDir = filepath.Join("..", "security35114", "testdata")

// start35114Loopback brings up the digest-less 35114 pairing at the given
// platform level.
func start35114Loopback(t *testing.T, level security35114.Level) *loopback {
	t.Helper()
	ctx := context.Background()

	mediaBase := 24000 + 100*freeUDPPort(t)%500
	sipPort := freeUDPPort(t)

	devIdentity, err := security35114.LoadIdentityFromFiles(
		filepath.Join(gb35114FixtureDir, "device_cert.pem"),
		filepath.Join(gb35114FixtureDir, "device_key.pem"))
	require.NoError(t, err, "device identity")
	platIdentity, err := security35114.LoadIdentityFromFiles(
		filepath.Join(gb35114FixtureDir, "platform_cert.pem"),
		filepath.Join(gb35114FixtureDir, "platform_key.pem"))
	require.NoError(t, err, "platform identity")

	plat35114, err := security35114.NewPlatform(security35114.PlatformConfig{
		ServerID:    lbServerID,
		Identity:    platIdentity,
		DeviceCerts: map[string]*smx509.Certificate{lbDeviceID: devIdentity.Certificate},
		Level:       level,
	})
	require.NoError(t, err, "NewPlatform")

	cfg := gbsip.Config{
		SIPListen:             fmt.Sprintf("127.0.0.1:%d", sipPort),
		ServerID:              lbServerID,
		Realm:                 lbDomain,
		PortRange:             fmt.Sprintf("%d-%d", mediaBase, mediaBase+99),
		MediaTransport:        "udp",
		RegisterAuthenticator: plat35114,
	}
	dm := platform.NewDeviceManager(60 * time.Second)
	sm := platform.NewSessionManager(platform.NewPortManager(uint16(mediaBase), uint16(mediaBase+99)), cfg.ServerID)
	psrv := gbsip.NewServer(cfg, dm, sm, nil)
	require.NoError(t, psrv.Start(ctx))
	t.Cleanup(func() { _ = psrv.Stop() })

	deviceAuth, err := security35114.New(security35114.Options{
		Device:       devIdentity,
		PlatformCert: platIdentity.Certificate,
		DeviceID:     lbDeviceID,
		ServerID:     lbServerID,
	})
	require.NoError(t, err, "device authenticator")

	fh := device.NewFrameHub()
	dcfg := device.Config{
		Enabled:               true,
		PlatformSIPAddress:    "127.0.0.1",
		PlatformSIPPort:       sipPort,
		DeviceID:              lbDeviceID,
		ChannelID:             lbChannelID,
		SIPDomain:             lbDomain,
		LocalSIPPort:          freeUDPPort(t),
		RegisterIntervalSecs:  3600,
		HeartbeatIntervalSecs: 60,
		HeartbeatTimeoutCount: 3,
		Transport:             "udp",
		RegisterAuthenticator: deviceAuth,
		// IncomingNotePolicy stays at the default Reject: every downstream
		// request's Note must verify or the dialog dies — that is the test.
	}
	dsrv := device.New(dcfg, device.DeviceInfo{
		Name: "GB35114 Cam", Manufacturer: "MiBee", Model: "loopback",
		Firmware: "gb35114", SerialNumber: "gb35114-sn",
	}, fh)
	startErr := make(chan error, 1)
	go func() { startErr <- dsrv.Start(ctx) }()
	t.Cleanup(func() {
		dsrv.Stop()
		select {
		case err := <-startErr:
			require.NoError(t, err, "device server exited with error")
		case <-time.After(5 * time.Second):
			t.Error("device server did not exit after Stop")
		}
	})

	return &loopback{platformSrv: psrv, devices: dm, sessions: sm, deviceSrv: dsrv, frames: fh}
}

// run35114MediaFlow drives the shared B/C scenario: register, catalog,
// INVITE, three AUs pushed through the hub, byte-exact arrival, zero
// rejected signatures.
func run35114MediaFlow(t *testing.T, level security35114.Level) {
	lb := start35114Loopback(t, level)
	lb.onlineDevice(t)
	lb.channelOf(t)

	require.NoError(t, lb.platformSrv.InviteChannel(lbDeviceID, lbChannelID))

	var mu sync.Mutex
	var gotAUs [][][]byte
	hub := lb.awaitHub(t)
	require.NoError(t, hub.Subscribe("gb35114", func(pts int64, au [][]byte, isIDR bool) {
		mu.Lock()
		defer mu.Unlock()
		gotAUs = append(gotAUs, au)
	}))

	lb.frames.Write(device.AccessUnit{NALUs: []device.NALU{nalu(7, lbSPS), nalu(8, lbPPS), nalu(5, lbIDR)}, KeyFrame: true})
	lb.frames.Write(device.AccessUnit{NALUs: []device.NALU{nalu(1, lbP)}, Timestamp: time.Now()})
	lb.frames.Write(device.AccessUnit{NALUs: []device.NALU{nalu(1, lbP)}, Timestamp: time.Now()})

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(gotAUs) >= 3
	}, 15*time.Second, 100*time.Millisecond, "all three signed%s AUs must arrive; got %d",
		map[bool]string{true: " and encrypted", false: ""}[level == security35114.LevelC], len(gotAUs))

	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, [][]byte{lbSPS, lbPPS, lbIDR}, gotAUs[0], "IDR AU must round-trip byte-exact through level "+string(level))
	require.Equal(t, [][]byte{lbP}, gotAUs[1], "first P AU must round-trip byte-exact")
	require.Equal(t, [][]byte{lbP}, gotAUs[2], "second P AU must round-trip byte-exact")

	if rc := lb.sessions.GetReceiver(lbChannelID); rc != nil {
		decrypt, verify := rc.MediaSecurityArmed()
		require.True(t, verify, "AU-signature verifier must be armed at level "+string(level))
		require.Equal(t, level == security35114.LevelC, decrypt,
			"payload decryptor armed state must match level "+string(level))
		require.Zero(t, rc.AUVerifyFailures(), "no AU signature may be rejected at level "+string(level))
	}

	require.NoError(t, lb.platformSrv.ByeChannel(lbDeviceID, lbChannelID))
}

func TestGB35114LevelB_MediaSignatures(t *testing.T) {
	run35114MediaFlow(t, security35114.LevelB)
}

func TestGB35114LevelC_EncryptedMedia(t *testing.T) {
	run35114MediaFlow(t, security35114.LevelC)
}
