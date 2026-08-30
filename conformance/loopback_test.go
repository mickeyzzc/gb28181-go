package conformance

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/mickeyzzc/gb28181-go/device"
	"github.com/mickeyzzc/gb28181-go/platform"
	"github.com/mickeyzzc/gb28181-go/platform/sip"
	"github.com/stretchr/testify/require"
)

// Loopback identities (GB/T 28181-2016 numbering plan).
const (
	lbDomain    = "3402000000"
	lbServerID  = "34020000002000000001"
	lbDeviceID  = "34020000001320000001"
	lbChannelID = "34020000001310000001"
	lbPassword  = "conformance-pw"
)

func freeUDPPort(t *testing.T) int {
	t.Helper()
	c, err := net.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	defer c.Close()
	return c.LocalAddr().(*net.UDPAddr).Port
}

// loopback brings up one platform SIP server and one device pointing at it.
type loopback struct {
	platformSrv *sip.Server
	devices     *platform.DeviceManager
	sessions    *platform.SessionManager
	deviceSrv   *device.Server
	frames      *device.FrameHub
}

func startLoopback(t *testing.T, heartbeat time.Duration) *loopback {
	t.Helper()
	ctx := context.Background()

	mediaBase := 22000 + 100*freeUDPPort(t)%500
	sipPort := freeUDPPort(t)

	cfg := sip.Config{
		SIPListen:      fmt.Sprintf("127.0.0.1:%d", sipPort),
		ServerID:       lbServerID,
		Realm:          lbDomain,
		Password:       lbPassword,
		PortRange:      fmt.Sprintf("%d-%d", mediaBase, mediaBase+99),
		MediaTransport: "udp",
	}
	dm := platform.NewDeviceManager(heartbeat)
	sm := platform.NewSessionManager(platform.NewPortManager(uint16(mediaBase), uint16(mediaBase+99)), cfg.ServerID)
	psrv := sip.NewServer(cfg, dm, sm, nil)
	require.NoError(t, psrv.Start(ctx))
	t.Cleanup(func() { _ = psrv.Stop() })

	fh := device.NewFrameHub()
	dcfg := device.Config{
		Enabled:               true,
		PlatformSIPAddress:    "127.0.0.1",
		PlatformSIPPort:       sipPort,
		DeviceID:              lbDeviceID,
		ChannelID:             lbChannelID,
		SIPDomain:             lbDomain,
		Password:              lbPassword,
		LocalSIPPort:          freeUDPPort(t),
		RegisterIntervalSecs:  3600,
		HeartbeatIntervalSecs: 1,
		HeartbeatTimeoutCount: 3,
		Transport:             "udp",
	}
	if heartbeat > 0 {
		// Keep the liveness checker fast enough to observe a missed keepalive
		// within the test's lifetime, aligned with the 1s device cadence.
		dcfg.HeartbeatIntervalSecs = int(heartbeat.Seconds())
	}
	dsrv := device.New(dcfg, device.DeviceInfo{
		Name:         "Conformance Cam",
		Manufacturer: "MiBee",
		Model:        "loopback",
		Firmware:     "conf-1",
		SerialNumber: "conf-sn-1",
	}, fh)
	// device.Server.Start runs its SIP receive loop in the caller's
	// goroutine and only returns on error/context-cancel — run it like a
	// host would; registration success is the readiness signal.
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

// onlineDevice polls the platform registry until the device shows up online.
func (lb *loopback) onlineDevice(t *testing.T) *platform.Device {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if d, ok := lb.devices.Device(lbDeviceID); ok && d.Status.Load() == platform.DeviceOnline {
			return d
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("device never registered online at the platform")
	return nil
}

// channelOf polls until the catalog exchange registered the device channel.
func (lb *loopback) channelOf(t *testing.T) *platform.Channel {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		for _, ch := range lb.devices.Channels(lbDeviceID) {
			if ch.ID == lbChannelID {
				return ch
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("catalog exchange never registered the channel at the platform")
	return nil
}

// nalu builds a device.NALU from raw payload bytes.
func nalu(t byte, data []byte) device.NALU {
	return device.NALU{Type: t, Data: data, IsIDR: t == 5, IsSPS: t == 7, IsPPS: t == 8}
}

var (
	lbSPS = []byte{0x67, 0x42, 0x00, 0x0a, 0xe2, 0x40, 0x40, 0x04, 0x00, 0x00, 0x00, 0x04, 0x00, 0x00, 0x00, 0xc8, 0x40}
	lbPPS = []byte{0x68, 0xce, 0x38, 0x80}
	lbIDR = []byte{0x65, 0x88, 0x84, 0x00, 0x01, 0x02, 0x03}
	lbP   = []byte{0x41, 0x9A, 0x33, 0x44}
)

// TestLoopback_RegisterCatalogKeepalive covers the signaling baseline:
// REGISTER with digest auth, post-REGISTER catalog exchange, and keepalive
// liveness (the platform's checker would mark the device offline within
// 3 missed heartbeats — staying online proves the keepalives arrive).
func TestLoopback_RegisterCatalogKeepalive(t *testing.T) {
	lb := startLoopback(t, 2*time.Second)

	dev := lb.onlineDevice(t)
	ch := lb.channelOf(t)
	require.Equal(t, lbChannelID, ch.ID)

	// The heartbeat interval is 2s with 3-strike offline: surviving 8s means
	// at least two keepalives landed.
	time.Sleep(8 * time.Second)
	require.Equal(t, platform.DeviceOnline, dev.Status.Load(), "keepalives must keep the device online")
}

// TestLoopback_InviteRoundTripBYE covers the media baseline: platform INVITEs
// the discovered channel, the device pushes RTP/PS from its FrameSource, and
// the platform's reassembled+demuxed access units must reproduce the pushed
// NAL bytes exactly — the byte-level round-trip through both roles' PS/RTP
// stacks (device psmux push → RTP → platform jitter reassembly → psdemux).
func TestLoopback_InviteRoundTripBYE(t *testing.T) {
	lb := startLoopback(t, 60*time.Second)
	lb.onlineDevice(t)
	lb.channelOf(t)

	require.NoError(t, lb.platformSrv.InviteChannel(lbDeviceID, lbChannelID))

	// Subscribe on the platform side before pushing frames.
	var mu sync.Mutex
	var gotAUs [][][]byte
	hub := lb.awaitHub(t)
	require.NoError(t, hub.Subscribe("conformance", func(pts int64, au [][]byte, isIDR bool) {
		mu.Lock()
		defer mu.Unlock()
		gotAUs = append(gotAUs, au)
	}))

	// Push one keyframe AU + two non-key AUs.
	lb.frames.Write(device.AccessUnit{NALUs: []device.NALU{nalu(7, lbSPS), nalu(8, lbPPS), nalu(5, lbIDR)}, KeyFrame: true})
	lb.frames.Write(device.AccessUnit{NALUs: []device.NALU{nalu(1, lbP)}, Timestamp: time.Now()})
	lb.frames.Write(device.AccessUnit{NALUs: []device.NALU{nalu(1, lbP)}, Timestamp: time.Now()})

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(gotAUs) >= 3
	}, 10*time.Second, 100*time.Millisecond, "all three access units must arrive; got %d", len(gotAUs))

	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, [][]byte{lbSPS, lbPPS, lbIDR}, gotAUs[0], "IDR AU must round-trip byte-exact (SPS/PPS/IDR)")
	require.Equal(t, [][]byte{lbP}, gotAUs[1], "first P AU must round-trip byte-exact")
	require.Equal(t, [][]byte{lbP}, gotAUs[2], "second P AU must round-trip byte-exact")

	// BYE tears the session down and frees the channel.
	require.NoError(t, lb.platformSrv.ByeChannel(lbDeviceID, lbChannelID))
	require.Eventually(t, func() bool {
		for _, ch := range lb.devices.Channels(lbDeviceID) {
			if ch.ID == lbChannelID {
				return ch.Status.Load() == platform.ChannelIdle
			}
		}
		return false
	}, 5*time.Second, 50*time.Millisecond, "channel must return to idle after BYE")
}

// awaitHub polls the session manager for the live session's frame hub.
func (lb *loopback) awaitHub(t *testing.T) *platform.FrameHub {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if h := lb.sessions.GetHub(lbChannelID); h != nil {
			return h
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("live session hub never appeared")
	return nil
}

// --- SIPS (GB/T 28181-2022 A-level): signaling over TLS ---------------------

// genSelfSignedCert writes a throwaway self-signed server certificate to
// dir and returns (certFile, keyFile).
func genSelfSignedCert(t *testing.T, dir string) (string, string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	tmpl := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "127.0.0.1"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	require.NoError(t, err)
	certFile := filepath.Join(dir, "cert.pem")
	keyFile := filepath.Join(dir, "key.pem")
	certOut := &bytes.Buffer{}
	require.NoError(t, pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: der}))
	require.NoError(t, os.WriteFile(certFile, certOut.Bytes(), 0o600))
	keyOut := &bytes.Buffer{}
	require.NoError(t, pem.Encode(keyOut, &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))
	require.NoError(t, os.WriteFile(keyFile, keyOut.Bytes(), 0o600))
	return certFile, keyFile
}

// TestLoopback_SIPSRegisterCatalog: the full signaling baseline over SIPS —
// the platform listens with a self-signed certificate, the device dials TLS
// (verifying via TLSCAFile), REGISTER+digest completes, and the platform's
// post-REGISTER catalog query reaches the device over the pooled TLS
// connection.
func TestLoopback_SIPSRegisterCatalog(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := genSelfSignedCert(t, dir)

	ctx := context.Background()
	sipPort := freeUDPPort(t)
	mediaBase := 23000 + 100*freeUDPPort(t)%400

	cfg := sip.Config{
		SIPListen:      fmt.Sprintf("127.0.0.1:%d", sipPort),
		ServerID:       lbServerID,
		Realm:          lbDomain,
		Password:       lbPassword,
		PortRange:      fmt.Sprintf("%d-%d", mediaBase, mediaBase+99),
		MediaTransport: "udp",
		SIPTransport:   "tls",
		TLSCertFile:    certFile,
		TLSKeyFile:     keyFile,
	}
	dm := platform.NewDeviceManager(60 * time.Second)
	sm := platform.NewSessionManager(platform.NewPortManager(uint16(mediaBase), uint16(mediaBase+99)), cfg.ServerID)
	psrv := sip.NewServer(cfg, dm, sm, nil)
	require.NoError(t, psrv.Start(ctx))
	t.Cleanup(func() { _ = psrv.Stop() })

	fh := device.NewFrameHub()
	dcfg := device.Config{
		Enabled:               true,
		PlatformSIPAddress:    "127.0.0.1",
		PlatformSIPPort:       sipPort,
		DeviceID:              lbDeviceID,
		ChannelID:             lbChannelID,
		SIPDomain:             lbDomain,
		Password:              lbPassword,
		LocalSIPPort:          freeUDPPort(t),
		RegisterIntervalSecs:  3600,
		HeartbeatIntervalSecs: 60,
		HeartbeatTimeoutCount: 3,
		Transport:             "tls",
		TLSCAFile:             certFile, // trust the platform's self-signed cert
	}
	dsrv := device.New(dcfg, device.DeviceInfo{Name: "SIPS Cam"}, fh)
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

	lb := &loopback{platformSrv: psrv, devices: dm, sessions: sm, deviceSrv: dsrv, frames: fh}
	dev := lb.onlineDevice(t)
	require.Equal(t, platform.DeviceOnline, dev.Status.Load())
	lb.channelOf(t) // catalog query + answer rode the TLS connection
}
