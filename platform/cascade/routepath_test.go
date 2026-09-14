package cascade

import (
	"context"
	"net"
	"strconv"
	"testing"

	"github.com/ghettovoice/gosip/sip"
	"github.com/mickeyzzc/gb28181-go/platform"
	"github.com/stretchr/testify/require"
)

// GB/T 28181-2022 Annex H.3: with RoutePathAnnounce configured, the
// cascade's INVITE 200 carries X-RoutePath naming the anchoring platform.
func TestInvite200AnnouncesRoutePath(t *testing.T) {
	src := fakeSource{cams: []CameraInfo{
		{ID: "cam-1", Name: "Front", Encoding: "h264"},
	}}
	hub := platform.NewFrameHub()
	src2 := hubSource{fakeSource: src, hub: hub}

	cfg := testCfg()
	cfg.SIPListen = net.JoinHostPort(lbLocalHost, strconv.Itoa(freeUDPPort(t)))
	up := newUpperSocket(t, cfg.SIPListen)
	cfg.ServerAddr = up.conn.LocalAddr().String()
	cfg.RoutePathAnnounce = "34020000002000000001"

	svc := New(cfg, src2, newCascadeTestDB(t))
	require.NoError(t, svc.Start(context.Background()))
	t.Cleanup(func() { _ = svc.Stop() })
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
	require.Equal(t, 200, int(res.StatusCode()))

	rp := res.GetHeaders("X-RoutePath")
	require.Len(t, rp, 1, "INVITE 200 must carry X-RoutePath when configured")
	require.Equal(t, "34020000002000000001", rp[0].(*sip.GenericHeader).Contents)

	byeID, ok := invite.CallID()
	require.True(t, ok)
	res = up.roundTrip(up.requestDialog(sip.BYE, lbChannelOne, "", "", byeID))
	require.Equal(t, 200, int(res.StatusCode()))
}

// Without RoutePathAnnounce the 200 keeps its prior wire form (no header).
func TestInvite200OmitsRoutePathByDefault(t *testing.T) {
	src := hubSource{
		fakeSource: fakeSource{cams: []CameraInfo{
			{ID: "cam-1", Name: "Front", Encoding: "h264"},
		}},
		hub: platform.NewFrameHub(),
	}
	svc, up := startLoopbackService(t, src, newCascadeTestDB(t))
	_, err := svc.catalogItems()
	require.NoError(t, err)

	res := up.roundTrip(up.request(sip.INVITE, lbChannelOne, inviteSDPFor(t, up), "application/sdp"))
	require.Equal(t, 200, int(res.StatusCode()))
	require.Empty(t, res.GetHeaders("X-RoutePath"))
}
