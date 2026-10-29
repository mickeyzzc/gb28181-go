//go:build gb35114

package conformance

// GB 35114 level B/C end-to-end loopbacks: a real device.Server (security35114
// Authenticator) registers against a real platform/sip.Server (security35114
// Platform at level B or C). Covers every media direction at C — live
// (UDP and TCP-passive), playback/download fetch, talk downlink + return
// uplink — plus the level-B signed live path.
//
// The signaling path doubles as the downstream-integrity proof: every
// platform→device request (catalog MESSAGE, INVITE, BYE) carries the keyed
// Date+Note pair, and the device's default IncomingNotePolicy (Reject) fails
// the whole suite if any of them does not verify.

import (
	"bytes"
	"context"
	"fmt"
	"os"
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
// platform level and media transport.
func start35114Loopback(t *testing.T, level security35114.Level, mediaTransport string) *loopback {
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
		MediaTransport:        mediaTransport,
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

// run35114MediaFlow drives the shared live scenario: register, catalog,
// INVITE, three AUs pushed through the hub, byte-exact arrival, zero
// rejected signatures.
func run35114MediaFlow(t *testing.T, level security35114.Level, mediaTransport string) {
	lb := start35114Loopback(t, level, mediaTransport)
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

	// Snapshot under the callback's mutex: reading len(gotAUs) at the
	// Eventually call site (message args are evaluated eagerly) races the
	// subscriber goroutine's append — the race detector poisoned this
	// suite on CI's first run of these loopbacks.
	auCount := func() int {
		mu.Lock()
		defer mu.Unlock()
		return len(gotAUs)
	}
	require.Eventually(t, func() bool { return auCount() >= 3 },
		15*time.Second, 100*time.Millisecond,
		"all three signed%s AUs must arrive",
		map[bool]string{true: " and encrypted", false: ""}[level == security35114.LevelC])

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
	run35114MediaFlow(t, security35114.LevelB, "udp")
}

func TestGB35114LevelC_EncryptedMedia(t *testing.T) {
	run35114MediaFlow(t, security35114.LevelC, "udp")
}

// TestGB35114LevelC_EncryptedMediaTCP is the C-level live flow over
// TCP-passive media: the device dials the platform's offered TCP port and
// the encrypted payloads ride RFC 4571 framing.
func TestGB35114LevelC_EncryptedMediaTCP(t *testing.T) {
	run35114MediaFlow(t, security35114.LevelC, platform.MediaTCPPassive)
}

// TestGB35114LevelC_Talk covers the platform→device talk downlink and the
// device→platform return uplink at level C: the talk INVITE offer carries
// the sealed VEK, the device decrypts and signature-verifies the downlink
// audio before the sink sees it, and the platform does the same for the
// return audio before OnTalkUplink fires.
func TestGB35114LevelC_Talk(t *testing.T) {
	lb := start35114Loopback(t, security35114.LevelC, "udp")
	lb.onlineDevice(t)
	lb.channelOf(t)

	downlink := make(chan []byte, 16)
	lb.deviceSrv.SetTalkbackSink(talkSinkFunc(func(payload []byte, ssrc uint32, codec device.AudioCodec) {
		select {
		case downlink <- payload:
		default:
		}
	}))
	upstream := make(chan []byte, 16)
	lb.deviceSrv.SetTalkbackSource(upstream)

	uplink := make(chan []byte, 16)
	lb.platformSrv.OnTalkUplink = func(channelID string, payload []byte) {
		select {
		case uplink <- payload:
		default:
		}
	}

	require.NoError(t, lb.platformSrv.StartTalk("cam-gb35114", lbDeviceID, lbChannelID))

	// Downlink: the platform's encrypted+signed frame must surface at the
	// device sink as the original plaintext.
	frame := bytes.Repeat([]byte{0xD5}, 160)
	lb.platformSrv.WriteTalkAudio(lbChannelID, frame)
	select {
	case got := <-downlink:
		require.Equal(t, frame, got, "downlink frame must arrive decrypted and verified")
	case <-time.After(10 * time.Second):
		t.Fatal("no downlink audio reached the device sink")
	}

	// Uplink: the device's return audio (encrypted+signed by the same
	// session key) must surface at the platform's uplink callback as
	// plaintext. The sender paces at 20ms ticks.
	ret := bytes.Repeat([]byte{0x2A}, 160)
	select {
	case upstream <- ret:
	case <-time.After(5 * time.Second):
		t.Fatal("talkback source never accepted a frame")
	}
	select {
	case got := <-uplink:
		require.Equal(t, ret, got, "return audio must arrive decrypted and verified")
	case <-time.After(10 * time.Second):
		t.Fatal("no return audio reached the platform uplink callback")
	}

	require.NoError(t, lb.platformSrv.StopTalk(lbChannelID))
}

// --- C-level playback fetch -------------------------------------------------

// staticIndex is a device.RecordingIndex over pre-built synthetic segments.
type staticIndex struct {
	root string
	segs []device.SegmentMeta
}

func (i staticIndex) Lookup(startMs, endMs int64) []device.SegmentMeta {
	var out []device.SegmentMeta
	for _, s := range i.segs {
		if s.EndMS >= startMs && s.StartMS <= endMs {
			out = append(out, s)
		}
	}
	return out
}

func (i staticIndex) Root() string { return i.root }

// writeSyntheticRecording35114 writes one synthetic segment (bare Annex-B +
// .ts.jsonl sidecar, the reference device segment format).
func writeSyntheticRecording35114(t *testing.T, dir string) (device.SegmentMeta, time.Time) {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0o755))
	base := time.Now().Add(-time.Hour).Truncate(time.Second)

	type fr struct {
		offset time.Duration
		key    bool
	}
	frames := []fr{{0, true}, {100 * time.Millisecond, false}, {200 * time.Millisecond, false}}
	var annexB, sidecar bytes.Buffer
	for _, f := range frames {
		types := []byte{1}
		if f.key {
			types = []byte{7, 8, 5}
		}
		for _, ty := range types {
			annexB.Write([]byte{0x00, 0x00, 0x00, 0x01, ty, 0x01, 0x02, 0x03})
		}
		fmt.Fprintf(&sidecar, "{\"pts_ms\":%d}\n", f.offset.Milliseconds())
	}
	file := filepath.Join(dir, "000001.h264")
	require.NoError(t, os.WriteFile(file, annexB.Bytes(), 0o644))
	require.NoError(t, os.WriteFile(file+".ts.jsonl", sidecar.Bytes(), 0o644))
	last := frames[len(frames)-1].offset
	return device.SegmentMeta{
		File: "000001.h264", StartMS: base.UnixMilli(),
		EndMS: base.Add(last).UnixMilli(), Size: int64(annexB.Len()),
		Frames: len(frames), Keyframes: 1,
	}, base
}

// playbackEnroller stubs the CameraEnroller seam so the platform's playback
// fetch resolves a bound camera and an AU sink.
type playbackEnroller struct {
	mu  sync.Mutex
	aus [][][]byte
}

func (e *playbackEnroller) EnsureGB28181Camera(deviceID, channelID, name, sourceIP string) error {
	return nil
}

func (e *playbackEnroller) GB28181CameraIDByChannel(deviceID, channelID string) (string, bool) {
	return "cam-gb35114-pb", true
}

func (e *playbackEnroller) GB28181NALUWriter(cameraID string) func(au [][]byte, ptsTicks int64, isIDR bool) {
	return nil
}

func (e *playbackEnroller) GB28181AudioWriter(cameraID string) func(codec string, data, config []byte, ptsTicks int64, samples int) {
	return nil
}
func (e *playbackEnroller) OnGB28181Invite(cameraID string)                       {}
func (e *playbackEnroller) OnGB28181Bye(cameraID string)                          {}
func (e *playbackEnroller) ArchiveGB28181Camera(deviceID, channelID string) error { return nil }
func (e *playbackEnroller) GB28181RecordingWanted(deviceID, channelID string) bool {
	return true
}
func (e *playbackEnroller) GB28181SubChannelID(deviceID, channelID string) string { return "" }
func (e *playbackEnroller) SetGB28181SubChannel(deviceID, channelID, subChannelID string) error {
	return nil
}

func (e *playbackEnroller) GB28181PlaybackAudioWriter(cameraID string) func(codec string, data, config []byte, ptsTicks int64, samples int) {
	return nil
}

func (e *playbackEnroller) UpdateGB28181DeviceMeta(deviceID, manufacturer, model string) error {
	return nil
}

func (e *playbackEnroller) NewGB28181PlaybackSink(cameraID string) (platform.AUWriter, error) {
	return auCollector{e}, nil
}

type auCollector struct{ e *playbackEnroller }

func (c auCollector) WriteNALU(au [][]byte, ptsTicks int64, isIDR bool) {
	c.e.mu.Lock()
	defer c.e.mu.Unlock()
	c.e.aus = append(c.e.aus, au)
}

// TestGB35114LevelC_Playback covers the device-recording fetch at level C:
// the s=Playback INVITE carries the a=security offer, the device's answer
// returns the sealed VEK, and the recorded stream arrives decrypted,
// signature-verified and byte-exact in the fetch sink.
func TestGB35114LevelC_Playback(t *testing.T) {
	lb := start35114Loopback(t, security35114.LevelC, "udp")
	lb.onlineDevice(t)
	lb.channelOf(t)

	recDir := filepath.Join(t.TempDir(), "rec")
	seg, _ := writeSyntheticRecording35114(t, recDir)
	lb.deviceSrv.SetRecordingIndex(staticIndex{root: recDir, segs: []device.SegmentMeta{seg}})

	enrol := &playbackEnroller{}
	lb.platformSrv.SetCameraEnroller(enrol)

	end := time.UnixMilli(seg.EndMS).Add(2 * time.Second)
	require.NoError(t, lb.platformSrv.StartPlayback(lbDeviceID, lbChannelID, time.UnixMilli(seg.StartMS), end))

	// auCollector guards e.aus with e.mu — read through the SAME mutex
	// (a test-local mutex here would race the collector goroutine).
	require.Eventually(t, func() bool {
		enrol.mu.Lock()
		defer enrol.mu.Unlock()
		return len(enrol.aus) >= 1
	}, 15*time.Second, 100*time.Millisecond, "fetched AUs must arrive at the playback sink")

	enrol.mu.Lock()
	defer enrol.mu.Unlock()
	first := enrol.aus[0]
	require.GreaterOrEqual(t, len(first), 3, "first AU should carry SPS/PPS/IDR NALUs")
	require.Equal(t, byte(0x07), first[0][0], "first NALU is SPS (type 7)")
	require.Equal(t, byte(0x08), first[1][0], "second NALU is PPS (type 8)")
	require.Equal(t, byte(0x05), first[2][0], "third NALU is IDR (type 5)")
	for _, nalu := range first {
		require.Equal(t, []byte{nalu[0], 0x01, 0x02, 0x03}, nalu, "NALU payload byte-exact")
	}

	if rc := lb.sessions.GetPlaybackReceiver(lbChannelID); rc != nil {
		decrypt, verify := rc.MediaSecurityArmed()
		require.True(t, decrypt, "playback fetch decryptor must be armed at C")
		require.True(t, verify, "playback fetch verifier must be armed at C")
		require.Zero(t, rc.AUVerifyFailures(), "no fetch AU signature may be rejected")
	}
}
