package cascade

// Coverage backfill: the keepalive wire path (registerLoop's heartbeat
// arm), the playback teardown path used by Service.Stop, and the small
// pure helpers (orDefault, errString, firstByteOr, addrHost).

import (
	"context"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/mickeyzzc/gb28181-go/manscdp"
	"github.com/mickeyzzc/gb28181-go/platform"
	"github.com/stretchr/testify/require"
)

// startLoopbackServiceFastHB boots a loopback service with a fast
// keepalive cadence so the heartbeat arm of registerLoop runs inside a
// test-budget-friendly window.
func startLoopbackServiceFastHB(t *testing.T) (*Service, *upperSocket) {
	t.Helper()

	cfg := testCfg()
	cfg.SIPListen = net.JoinHostPort(lbLocalHost, strconv.Itoa(freeUDPPort(t)))
	cfg.HeartbeatInterval = "200ms"

	up := newUpperSocket(t, cfg.SIPListen)
	cfg.ServerAddr = up.conn.LocalAddr().String()

	hub := platform.NewFrameHub()
	svc := New(cfg, hubSource{fakeSource{cams: []CameraInfo{{ID: "cam-1", Name: "Front"}}}, hub}, newCascadeTestDB(t))
	svc.SetSegmentParser(fakeSegmentParser)
	require.NoError(t, svc.Start(context.Background()))
	t.Cleanup(func() { _ = svc.Stop() })

	return svc, up
}

func TestLoopbackKeepaliveMessagesFlow(t *testing.T) {
	svc, up := startLoopbackServiceFastHB(t)

	stop := make(chan struct{})
	serveRegistration(t, up, stop)
	defer close(stop)

	require.Eventually(t, func() bool { return svc.Online() }, 10*time.Second, 100*time.Millisecond,
		"service must register first")

	// The keepalive cadence sends Keepalive MESSAGEs to the upper socket;
	// serveRegistration answers 200 so the registration stays online.
	require.Eventually(t, func() bool {
		svc.mu.Lock()
		uppers := len(svc.uppers)
		svc.mu.Unlock()
		if uppers == 0 {
			return false
		}

		// Drain any pending keepalive from the socket; a Keepalive body
		// proves the heartbeat arm executed.
		_ = up.conn.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
		buf := make([]byte, 65535)
		for {
			n, _, err := up.conn.ReadFromUDP(buf)
			if err != nil {
				break
			}
			if n > 0 && containsKeepaliveBody(string(buf[:n])) {
				return true
			}
		}
		return false
	}, 10*time.Second, 100*time.Millisecond, "keepalive MESSAGE must reach the upper platform")

	require.True(t, svc.Online(), "answered keepalives keep the registration online")
}

func containsKeepaliveBody(raw string) bool {
	keepalive, err := manscdp.Encode(manscdp.Keepalive{
		CmdType: manscdp.CmdKeepalive, SN: 1, DeviceID: "x", Status: "OK",
	})
	if err != nil {
		return false
	}

	// Keepalive bodies carry the CmdType element; raw wire check keeps
	// this independent of exact SN/device values.
	for _, marker := range []string{"<CmdType>Keepalive</CmdType>", `CmdType="Keepalive"`} {
		if len(raw) >= len(keepalive) && indexof(raw, marker) >= 0 {
			return true
		}
	}

	return false
}

func indexof(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}

	return -1
}

func TestPlaybackSessionStopTeardown(t *testing.T) {
	// A UDP conn stands in for the media socket; stop() must close the
	// done channel exactly once and close the connection.
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	require.NoError(t, err)

	ps := &playbackSession{
		svc:    &Service{},
		callID: "stop-1",
		done:   make(chan struct{}),
		conn:   conn,
	}

	ps.stop()

	select {
	case <-ps.done:
	default:
		t.Fatal("stop must close the done channel")
	}

	// A closed UDP conn errors on write.
	_, err = conn.WriteToUDP([]byte("x"), &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 9})
	require.Error(t, err, "media socket must be closed")

	// Second stop is a no-op (CompareAndSwap guard) — no double close.
	ps.stop()
}

func TestSmallPureHelpers(t *testing.T) {
	require.Equal(t, "fallback", orDefault("", "fallback"))
	require.Equal(t, "set", orDefault("set", "fallback"))

	require.Equal(t, "no source", errString(nil))
	require.Equal(t, "x", errString(errTest("x")))

	require.Equal(t, byte(0), firstByteOr(nil))
	require.Equal(t, byte(0xAB), firstByteOr([]byte{0xAB, 0xCD}))

	require.Equal(t, "127.0.0.1", addrHost("127.0.0.1:5060"))
	require.Equal(t, "127.0.0.1", addrHost("127.0.0.1"))
}

type errTest string

func (e errTest) Error() string { return string(e) }
