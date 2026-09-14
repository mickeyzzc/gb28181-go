package cascade

import (
	"net"
	"strconv"
	"testing"

	"github.com/ghettovoice/gosip/sip"
	"github.com/mickeyzzc/gb28181-go/manscdp"
	"github.com/mickeyzzc/gb28181-go/platform"
	"github.com/stretchr/testify/require"
)

// Multi-level loop prevention (issue #77): channels learned FROM an upper
// (the upper platform also registers into us as a device) must never be
// echoed back to that upper — neither in catalog answers/notifications nor
// by accepting an INVITE for them. Loop topology stand-in: cam-echo's
// OriginDeviceID equals the upper's platform ID (testCfg().ServerDomain).

func loopGuardSource() hubSource {
	return hubSource{fakeSource{cams: []CameraInfo{
		{ID: "cam-local", Name: "Front", Encoding: "h264"},
		{ID: "cam-echo", Name: "From Upper", OriginDeviceID: testCfg().ServerDomain},
	}}, platform.NewFrameHub()}
}

func TestLoopbackCatalogExcludesUpperOriginChannels(t *testing.T) {
	db := newCascadeTestDB(t)
	svc, up := startLoopbackService(t, loopGuardSource(), db)
	_, err := svc.catalogItems()
	require.NoError(t, err)

	body, err := manscdp.Encode(manscdp.CatalogQuery{CmdType: manscdp.CmdCatalog, SN: 21})
	require.NoError(t, err)
	res := up.roundTrip(up.request(sip.MESSAGE, lbChannelOne, string(body), "Application/MANSCDP+xml"))
	require.Equal(t, 200, int(res.StatusCode()))

	answer := up.awaitServerRequest(sip.MESSAGE, "<CmdType>Catalog</CmdType>")
	got := string(answer.Body())
	require.Contains(t, got, "<SN>21</SN>")
	require.Contains(t, got, "<Name>Front</Name>", "local camera stays in the catalog")
	require.NotContains(t, got, "From Upper", "channels learned from this upper must be excluded")
	require.Contains(t, got, "<SumNum>1</SumNum>", "SumNum counts only the non-excluded channels")
}

func TestLoopbackInviteEchoChannelRefused(t *testing.T) {
	db := newCascadeTestDB(t)
	svc, up := startLoopbackService(t, loopGuardSource(), db)
	// The unfiltered internal view still allocates and exposes the echo
	// channel (the upper never sees it in its catalog answer).
	all, err := svc.catalogItems()
	require.NoError(t, err)
	echoCh := ""
	for _, it := range all {
		if it.Name == "From Upper" {
			echoCh = it.DeviceID
		}
	}
	require.NotEmpty(t, echoCh, "echo channel must be allocated internally")
	media, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	require.NoError(t, err)
	defer media.Close()
	sdp := "v=0\r\no=" + lbUpperDevice + " 0 0 IN IP4 " + lbLocalHost + "\r\ns=Play\r\n" +
		"c=IN IP4 " + lbLocalHost + "\r\nt=0 0\r\n" +
		"m=video " + strconv.Itoa(media.LocalAddr().(*net.UDPAddr).Port) + " RTP/AVP 96\r\ny=4242\r\n"

	res := up.roundTrip(up.request(sip.INVITE, echoCh, sdp, "application/sdp"))
	require.Equal(t, 404, int(res.StatusCode()), "INVITE for an upper-origin channel must be refused like unknown")

	// The local channel keeps working through the same upper.
	res = up.roundTrip(up.request(sip.INVITE, lbChannelOne, sdp, "application/sdp"))
	require.Equal(t, 200, int(res.StatusCode()), "local channel INVITE must still succeed")
	require.Equal(t, 1, svc.ForwardCount())
}

// Unrelated origins stay visible: only the requesting upper's own origin is
// excluded — a channel from a THIRD downstream keeps flowing to every upper.
func TestCatalogKeepsThirdPartyOrigin(t *testing.T) {
	svc := New(testCfg(), hubSource{fakeSource{cams: []CameraInfo{
		{ID: "cam-third", Name: "From C", OriginDeviceID: "34020000002000000099"},
	}}, platform.NewFrameHub()}, newCascadeTestDB(t))
	items, err := svc.catalogItemsFor(svc.uppers[0])
	require.NoError(t, err)
	require.Len(t, items, 1, "a third-party origin must NOT be excluded from this upper's catalog")
}
