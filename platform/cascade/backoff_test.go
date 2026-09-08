package cascade

// Register-retry exponential backoff (issue #44): consecutive REGISTER
// failures wait base, 2×base, … capped at max; a successful registration
// resets the sequence. Verified over the wire by timestamping the
// authorized REGISTER attempts the fake upper receives.

import (
	"context"
	"net"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/ghettovoice/gosip/sip"
	"github.com/mickeyzzc/gb28181-go/platform"
	"github.com/stretchr/testify/require"
)

// rejectingRegistrar plays the upper platform: it challenges the first
// REGISTER per attempt (digest dance), answers the authorized REGISTER
// with 403 for the first rejections attempts, 200 afterwards, and
// timestamps every authorized arrival.
type rejectingRegistrar struct {
	t          *testing.T
	up         *upperSocket
	rejections int
	stop       chan struct{}
	mu         sync.Mutex
	arrivals   []time.Duration // since start
	authorized int
}

func startRejectingRegistrar(t *testing.T, up *upperSocket, rejections int) *rejectingRegistrar {
	t.Helper()

	r := &rejectingRegistrar{
		t: t, up: up, rejections: rejections,
		stop: make(chan struct{}),
	}
	go r.loop()
	t.Cleanup(func() { close(r.stop) })
	return r
}

func (r *rejectingRegistrar) loop() {
	start := time.Now()
	buf := make([]byte, 65535)
	for {
		select {
		case <-r.stop:
			return
		default:
		}
		_ = r.up.conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
		n, src, err := r.up.conn.ReadFromUDP(buf)
		if err != nil {
			continue
		}
		msg, err := parseSIPBytes(buf[:n])
		if err != nil {
			continue
		}
		req, ok := msg.(sip.Request)
		if !ok || req.Method() != sip.REGISTER {
			// Keepalives etc. during the online phase: accept.
			if ok {
				_, _ = r.up.conn.WriteToUDP(challengeResponse(r.t, req, 200, "OK", ""), src)
			}
			continue
		}
		if len(req.GetHeaders("Authorization")) == 0 {
			chal := `WWW-Authenticate: Digest realm="34020000002000000001", nonce="bo-nonce", algorithm=MD5`
			_, _ = r.up.conn.WriteToUDP(challengeResponse(r.t, req, 401, "Unauthorized", chal), src)
			continue
		}
		r.mu.Lock()
		r.authorized++
		nth := r.authorized
		r.arrivals = append(r.arrivals, time.Since(start))
		r.mu.Unlock()
		if nth <= r.rejections {
			_, _ = r.up.conn.WriteToUDP(challengeResponse(r.t, req, 403, "Forbidden", ""), src)
		} else {
			_, _ = r.up.conn.WriteToUDP(challengeResponse(r.t, req, 200, "OK", ""), src)
		}
	}
}

func (r *rejectingRegistrar) arrivalGaps() []time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()
	gaps := make([]time.Duration, 0, len(r.arrivals)-1)
	for i := 1; i < len(r.arrivals); i++ {
		gaps = append(gaps, r.arrivals[i]-r.arrivals[i-1])
	}
	return gaps
}

func TestRegisterRetryBacksOffExponentially(t *testing.T) {
	cfg := testCfg()
	cfg.SIPListen = net.JoinHostPort(lbLocalHost, strconv.Itoa(freeUDPPort(t)))
	cfg.HeartbeatInterval = "500ms"
	cfg.RegisterRetryBase = "60ms"
	cfg.RegisterRetryMax = "240ms"

	up := newUpperSocket(t, cfg.SIPListen)
	cfg.ServerAddr = up.conn.LocalAddr().String()

	hub := platform.NewFrameHub()
	svc := New(cfg, hubSource{fakeSource{cams: []CameraInfo{{ID: "cam-1", Name: "Front"}}}, hub}, newCascadeTestDB(t))
	require.NoError(t, svc.Start(context.Background()))
	t.Cleanup(func() { _ = svc.Stop() })

	// Fail the first two attempts; the third registers.
	reg := startRejectingRegistrar(t, up, 2)

	require.Eventually(t, func() bool { return svc.Online() }, 10*time.Second, 50*time.Millisecond,
		"the third attempt must register")

	gaps := reg.arrivalGaps()
	require.GreaterOrEqual(t, len(gaps), 2, "need two retry gaps, got %v", gaps)
	require.Less(t, slices.Max(gaps), time.Second, "gaps %v must stay within the backoff budget", gaps)

	gap1, gap2 := gaps[0], gaps[1]
	require.GreaterOrEqual(t, gap1, 50*time.Millisecond,
		"first retry wait must honor the 60ms base (gap1=%v)", gap1)
	require.Greater(t, gap2, 2*gap1-30*time.Millisecond,
		"second retry wait must double the first (gap1=%v gap2=%v)", gap1, gap2)
}

func TestRegisterRetryBackoffResetsAfterSuccess(t *testing.T) {
	cfg := testCfg()
	cfg.SIPListen = net.JoinHostPort(lbLocalHost, strconv.Itoa(freeUDPPort(t)))
	cfg.HeartbeatInterval = "500ms"
	cfg.RegisterRetryBase = "60ms"
	cfg.RegisterRetryMax = "10s"

	up := newUpperSocket(t, cfg.SIPListen)
	cfg.ServerAddr = up.conn.LocalAddr().String()

	hub := platform.NewFrameHub()
	svc := New(cfg, hubSource{fakeSource{cams: []CameraInfo{{ID: "cam-1", Name: "Front"}}}, hub}, newCascadeTestDB(t))
	require.NoError(t, svc.Start(context.Background()))
	t.Cleanup(func() { _ = svc.Stop() })

	// One failure, then online.
	reg := startRejectingRegistrar(t, up, 1)

	require.Eventually(t, func() bool { return svc.Online() }, 10*time.Second, 50*time.Millisecond)

	gaps := reg.arrivalGaps()
	require.NotEmpty(t, gaps)
	require.Less(t, slices.Max(gaps), time.Second,
		"a fresh sequence after reset must start at the 60ms base again (gaps=%v)", gaps)
}
