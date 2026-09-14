package cascade

// HubActivator seam tests (multi-level cascade — MiBeeNvr #451): the upper
// platform's INVITE for a channel whose hub is idle must activate it through
// the injected activator, answer 200 only once the hub is real, forward
// media, and drop the activation reference at session teardown. Without an
// activator the legacy 500 stands.

import (
	"context"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/ghettovoice/gosip/sip"
	"github.com/mickeyzzc/gb28181-go/platform"
	"github.com/stretchr/testify/require"
)

// idleSource serves cameras but a nil hub until the activator swaps one in —
// the "GB28181 child camera that is not currently recording" stand-in.
type idleSource struct {
	fakeSource
	mu  sync.Mutex
	hub *platform.FrameHub
}

func (f *idleSource) Hub(string) *platform.FrameHub {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.hub
}

func (f *idleSource) setHub(h *platform.FrameHub) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hub = h
}

type fakeActivator struct {
	mu      sync.Mutex
	calls   int
	src     *idleSource
	hub     *platform.FrameHub
	release chan struct{}
	block   bool // timeout path: park on ctx instead of producing a hub
}

func (a *fakeActivator) EnsureHubActive(
	ctx context.Context, cameraID string,
) (*platform.FrameHub, func(), error) {
	a.mu.Lock()
	a.calls++
	a.mu.Unlock()
	if a.block {
		<-ctx.Done()
		return nil, nil, ctx.Err()
	}
	a.src.setHub(a.hub)
	return a.hub, func() { a.release <- struct{}{} }, nil
}

func (a *fakeActivator) callCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.calls
}

// An INVITE for an idle channel is answered 200 only after the activator
// produced the hub, media flows through the activated hub, and the BYE
// drops the activation reference.
func TestInviteActivatesIdleHub(t *testing.T) {
	src := &idleSource{fakeSource: fakeSource{cams: []CameraInfo{
		{ID: "cam-1", Name: "Front", Encoding: "h264"},
	}}}
	hub := platform.NewFrameHub()
	acq := &fakeActivator{src: src, hub: hub, release: make(chan struct{}, 1)}

	db := newCascadeTestDB(t)
	svc, up := startLoopbackService(t, src, db)
	svc.SetHubActivator(acq)
	_, err := svc.catalogItems()
	require.NoError(t, err)

	media, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	require.NoError(t, err)
	defer media.Close()

	sdp := "v=0\r\no=" + lbUpperDevice + " 0 0 IN IP4 " + lbLocalHost + "\r\ns=Play\r\n" +
		"c=IN IP4 " + lbLocalHost + "\r\nt=0 0\r\n" +
		"m=video " + strconv.Itoa(media.LocalAddr().(*net.UDPAddr).Port) + " RTP/AVP 96\r\ny=4242\r\n"
	invite := up.request(sip.INVITE, lbChannelOne, sdp, "application/sdp")
	res := up.roundTrip(invite)
	require.Equal(t, 200, int(res.StatusCode()), "INVITE must succeed after activation")
	require.Equal(t, 1, acq.callCount(), "activation must run exactly once")

	// Media flows through the activated hub (poll, never sleep — #571).
	buf := make([]byte, 2048)
	idr := [][]byte{{0x67, 0x64, 0x00, 0x1f}, {0x68, 0xeb, 0xe3, 0xcb}}
	require.Eventually(t, func() bool {
		hub.Broadcast(1000, idr, true)
		_ = media.SetReadDeadline(time.Now().Add(150 * time.Millisecond))
		n, _, err := media.ReadFromUDP(buf)
		return err == nil && n > 12 && buf[0]&0x80 == 0x80
	}, 5*time.Second, 50*time.Millisecond, "activated hub must feed the forward")

	// BYE tears the session down and drops the activation reference. The
	// close runs after the 200 is sent — poll the observable end state.
	byeID, ok := invite.CallID()
	require.True(t, ok)
	res = up.roundTrip(up.requestDialog(sip.BYE, lbChannelOne, "", "", byeID))
	require.Equal(t, 200, int(res.StatusCode()))
	require.Eventually(t, func() bool {
		select {
		case <-acq.release:
			return true
		default:
			return false
		}
	}, 3*time.Second, 20*time.Millisecond, "BYE must release the activation reference")
}

// A blocking activator that never produces a hub makes the INVITE fail with
// the legacy 500 once HubActivationTimeout elapses — the dialog never
// establishes, no session leaks.
func TestInviteActivationTimeout(t *testing.T) {
	src := &idleSource{fakeSource: fakeSource{cams: []CameraInfo{
		{ID: "cam-1", Name: "Front", Encoding: "h264"},
	}}}
	acq := &fakeActivator{
		src: src, hub: platform.NewFrameHub(),
		release: make(chan struct{}, 1), block: true,
	}

	cfg := testCfg()
	cfg.SIPListen = net.JoinHostPort(lbLocalHost, strconv.Itoa(freeUDPPort(t)))
	up := newUpperSocket(t, cfg.SIPListen)
	cfg.ServerAddr = up.conn.LocalAddr().String()
	cfg.HubActivationTimeout = "300ms"

	svc := New(cfg, src, newCascadeTestDB(t))
	svc.SetHubActivator(acq)
	require.NoError(t, svc.Start(context.Background()))
	t.Cleanup(func() { _ = svc.Stop() })
	_, err := svc.catalogItems()
	require.NoError(t, err)

	start := time.Now()
	res := up.roundTrip(up.request(sip.INVITE, lbChannelOne, inviteSDPFor(t, up), "application/sdp"))
	require.Equal(t, 500, int(res.StatusCode()), "activation timeout must answer 500")
	require.GreaterOrEqual(t, time.Since(start), 250*time.Millisecond, "must wait for the timeout")
	require.Less(t, time.Since(start), 5*time.Second, "must not park past the bounded timeout")
	require.Equal(t, 0, len(sessionIDs(svc)), "no session must leak")
}

// Without an activator the idle-hub INVITE keeps the legacy 500.
func TestInviteIdleNoActivator(t *testing.T) {
	src := &idleSource{fakeSource: fakeSource{cams: []CameraInfo{
		{ID: "cam-1", Name: "Front", Encoding: "h264"},
	}}}
	svc, up := startLoopbackService(t, src, newCascadeTestDB(t))
	_, err := svc.catalogItems()
	require.NoError(t, err)

	res := up.roundTrip(up.request(sip.INVITE, lbChannelOne, inviteSDPFor(t, up), "application/sdp"))
	require.Equal(t, 500, int(res.StatusCode()))
	require.Equal(t, 0, len(sessionIDs(svc)))
}

// inviteSDPFor builds a live INVITE SDP pointing at a throwaway UDP socket.
func inviteSDPFor(t *testing.T, up *upperSocket) string {
	t.Helper()
	media, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	require.NoError(t, err)
	t.Cleanup(func() { _ = media.Close() })
	return "v=0\r\no=" + lbUpperDevice + " 0 0 IN IP4 " + lbLocalHost + "\r\ns=Play\r\n" +
		"c=IN IP4 " + lbLocalHost + "\r\nt=0 0\r\n" +
		"m=video " + strconv.Itoa(media.LocalAddr().(*net.UDPAddr).Port) + " RTP/AVP 96\r\ny=4242\r\n"
}
