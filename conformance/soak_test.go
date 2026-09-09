package conformance

// Soak: long-run register + keepalive + INVITE/BYE cycles against the
// loopback pair, asserting no file-descriptor growth (the leak proxy for
// session/media socket teardown). Opt-in — normal CI stays fast:
//
//	GB28181_SOAK=1 go test -run TestSoak ./conformance/            (default 100 cycles)
//	GB28181_SOAK=1 GB28181_SOAK_CYCLES=500 go test -run TestSoak ./conformance/

import (
	"os"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/mickeyzzc/gb28181-go/device"
	"github.com/mickeyzzc/gb28181-go/platform"
	"github.com/stretchr/testify/require"
)

func soakCycles(t *testing.T) int {
	t.Helper()
	if os.Getenv("GB28181_SOAK") == "" {
		t.Skip("soak: set GB28181_SOAK=1 (optionally GB28181_SOAK_CYCLES=N)")
	}
	n := 100
	if v := os.Getenv("GB28181_SOAK_CYCLES"); v != "" {
		parsed, err := strconv.Atoi(v)
		require.NoError(t, err, "GB28181_SOAK_CYCLES must be an integer")
		n = parsed
	}
	return n
}

func fdCount(t *testing.T) int {
	t.Helper()
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Skip("fd counting needs /proc (linux)")
	}
	return len(entries)
}

// TestSoakInviteCycles drives the full live-media lifecycle N times on
// one loopback pair: the device stays registered (keepalives flowing at
// 1s cadence the whole time) while INVITE → frame round-trip → BYE
// cycles establish and tear down media sessions. Descriptors must not
// grow: every cycle closes the device media socket and recycles the
// platform media port.
func TestSoakInviteCycles(t *testing.T) {
	cycles := soakCycles(t)

	lb := startLoopback(t, time.Second)
	lb.onlineDevice(t)
	lb.channelOf(t)

	before := fdCount(t)

	for i := range cycles {
		require.NoError(t, lb.platformSrv.InviteChannel(lbDeviceID, lbChannelID),
			"cycle %d: INVITE", i)

		_ = lb.awaitHub(t) // session live before frames flow
		lb.frames.Write(device.AccessUnit{NALUs: []device.NALU{nalu(7, lbSPS), nalu(8, lbPPS), nalu(5, lbIDR)}, KeyFrame: true})
		lb.frames.Write(device.AccessUnit{NALUs: []device.NALU{nalu(1, lbP)}, Timestamp: time.Now()})

		require.NoError(t, lb.platformSrv.ByeChannel(lbDeviceID, lbChannelID),
			"cycle %d: BYE", i)
		require.Eventually(t, func() bool {
			for _, ch := range lb.devices.Channels(lbDeviceID) {
				if ch.ID == lbChannelID {
					return ch.Status.Load() == platform.ChannelIdle
				}
			}
			return false
		}, 5*time.Second, 20*time.Millisecond, "cycle %d: channel must return to idle", i)
	}

	// Settle: let late goroutines (media receivers, port recycles) finish.
	time.Sleep(2 * time.Second)
	runtime.GC()

	after := fdCount(t)
	t.Logf("soak: %d cycles, fds %d → %d", cycles, before, after)
	require.LessOrEqual(t, after, before+8,
		"descriptors grew by %d across %d INVITE/BYE cycles — a session/media socket leaks", after-before, cycles)
}
