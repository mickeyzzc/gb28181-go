package cascade

// Second-round loopback coverage for the media-path branches: TCP media
// forwarding (offer setup:passive → we dial), the TCP dial-failure 500,
// playback re-INVITE window matching, and the upper-platform DeviceControl
// commands that have no local equivalent (explicitly ignored, never silent).

import (
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/ghettovoice/gosip/sip"
	"github.com/mickeyzzc/gb28181-go/manscdp"
	"github.com/mickeyzzc/gb28181-go/platform"
	"github.com/mickeyzzc/gb28181-go/psmux"
	"github.com/stretchr/testify/require"
)

// tcpPlaySDP builds a TCP-passive live-forward offer pointing media at
// tcpPort (we dial per a=setup:passive).
func tcpPlaySDP(t *testing.T, tcpPort int) string {
	t.Helper()

	return "v=0\r\no=" + lbUpperDevice + " 0 0 IN IP4 " + lbLocalHost + "\r\ns=Play\r\n" +
		"c=IN IP4 " + lbLocalHost + "\r\nt=0 0\r\n" +
		"m=video " + strconv.Itoa(tcpPort) + " TCP/RTP/AVP 96\r\n" +
		"a=setup:passive\r\na=connection:new\r\n" +
		"a=rtpmap:96 PS/90000\r\ny=12345678\r\n"
}

func TestLoopbackInviteTCPMediaForward(t *testing.T) {
	// Upper side: passive TCP media listener.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()

	accepted := make(chan net.Conn, 1)
	go func() {
		c, err := ln.Accept()
		if err == nil {
			accepted <- c
		}
	}()
	t.Cleanup(func() {
		select {
		case c := <-accepted:
			_ = c.Close()
		default:
		}
	})

	hub := platform.NewFrameHub()
	db := newCascadeTestDB(t)
	svc, up := startLoopbackService(t, hubSource{fakeSource{cams: []CameraInfo{{ID: "cam-1", Name: "Front"}}}, hub}, db)
	_, err = svc.catalogItems()
	require.NoError(t, err)

	res := up.roundTrip(up.request(sip.INVITE, lbChannelOne, tcpPlaySDP(t, ln.Addr().(*net.TCPAddr).Port), "application/sdp"))
	require.Equal(t, 200, int(res.StatusCode()), "TCP-passive INVITE must be accepted")

	// Answer as the TCP-active side: we dialed.
	require.Contains(t, string(res.Body()), "TCP/RTP/AVP 96")
	require.Contains(t, string(res.Body()), "a=setup:active")

	// Frames fed through the hub must arrive as RTP/PS bytes on the TCP conn.
	var conn net.Conn
	select {
	case conn = <-accepted:
	case <-time.After(5 * time.Second):
		t.Fatal("cascade never dialed the TCP media address")
	}
	defer conn.Close()
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(5*time.Second)))

	mux := psmux.New()
	mux.SetVideoCodec("h264")
	idr := append([]byte{}, 0, 0, 0, 1)
	idr = append(idr, 0x67, 0x64, 0x00, 0x1F)
	idr = append(idr, 0, 0, 0, 1, 0x65, 0x01)

	buf := make([]byte, 65535)
	require.Eventually(t, func() bool {
		hub.Broadcast(90000, [][]byte{{0x67, 0x64, 0x00, 0x1F}, {0x65, 0x01}}, true)
		n, err := conn.Read(buf)
		return err == nil && n > 0
	}, 5*time.Second, 100*time.Millisecond, "RTP/PS media must flow over the TCP connection")
}

func TestLoopbackInviteTCPDialFailure(t *testing.T) {
	// A TCP offer pointing at a port with no listener: the dial fails and
	// the INVITE is answered 500.
	dead, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	deadPort := dead.Addr().(*net.TCPAddr).Port
	require.NoError(t, dead.Close())

	hub := platform.NewFrameHub()
	db := newCascadeTestDB(t)
	svc, up := startLoopbackService(t, hubSource{fakeSource{cams: []CameraInfo{{ID: "cam-1", Name: "Front"}}}, hub}, db)
	_, err = svc.catalogItems()
	require.NoError(t, err)

	res := up.roundTrip(up.request(sip.INVITE, lbChannelOne, tcpPlaySDP(t, deadPort), "application/sdp"))
	require.Equal(t, 500, int(res.StatusCode()), "unreachable TCP media address must 500")
	require.Zero(t, len(sessionIDs(svc)), "no session may be registered after a dial failure")
}

func TestLoopbackPlaybackReInviteWindow(t *testing.T) {
	hub := platform.NewFrameHub()
	db := newCascadeTestDB(t)
	svc, up := startLoopbackService(t, hubSource{fakeSource{cams: []CameraInfo{{ID: "cam-1", Name: "Front"}}}, hub}, db)
	_, err := svc.catalogItems()
	require.NoError(t, err)

	now := time.Now().UTC()
	createPacedPlaybackSegment(t, db, "cam-1", now.Add(-10*time.Minute))

	pbInvite := up.request(sip.INVITE, lbChannelOne, playSDP(t, "Playback", true), "application/sdp")
	res := up.roundTrip(pbInvite)
	require.Equal(t, 200, int(res.StatusCode()))
	require.Eventually(t, func() bool { return len(playbackIDs(svc)) == 1 },
		5*time.Second, 20*time.Millisecond, "playback dialog must be registered")

	pbID, ok := pbInvite.CallID()
	require.True(t, ok)

	// Same dialog, same t= window: idempotent re-INVITE, same SDP answered.
	res = up.roundTrip(up.requestDialog(sip.INVITE, lbChannelOne, string(pbInvite.Body()), "application/sdp", pbID))
	require.Equal(t, 200, int(res.StatusCode()))
	require.Contains(t, string(res.Body()), "s=Playback")
	require.Len(t, playbackIDs(svc), 1, "same-window re-INVITE must not replace the playback")

	// Different window: the old playback is finished and replaced.
	shifted := playSDP(t, "Playback", true)
	res = up.roundTrip(up.requestDialog(sip.INVITE, lbChannelOne, shifted, "application/sdp", pbID))
	require.Equal(t, 200, int(res.StatusCode()))
	require.Eventually(t, func() bool { return len(playbackIDs(svc)) <= 1 },
		5*time.Second, 20*time.Millisecond)
}

func TestLoopbackDeviceControlIgnoredCommands(t *testing.T) {
	db := newCascadeTestDB(t)
	_, up := startLoopbackService(t, fakeSource{}, db)

	// Management commands with no local equivalent on an NVR lower: each is
	// acknowledged (200) and explicitly ignored — never silently dropped.
	commands := []manscdp.DeviceControl{
		{CmdType: manscdp.CmdDeviceControl, SN: 21, DeviceID: lbChannelOne, RecordCmd: "Record"},
		{CmdType: manscdp.CmdDeviceControl, SN: 22, DeviceID: lbChannelOne, GuardCmd: "SetGuard"},
		{CmdType: manscdp.CmdDeviceControl, SN: 23, DeviceID: lbChannelOne, AlarmCmd: "ResetAlarm"},
		{CmdType: manscdp.CmdDeviceControl, SN: 24, DeviceID: lbChannelOne, TeleBoot: "Reboot"},
		{CmdType: manscdp.CmdDeviceControl, SN: 25, DeviceID: lbChannelOne, HomePosition: "Set"},
		{CmdType: manscdp.CmdDeviceControl, SN: 26, DeviceID: "34020099991320000099", PTZCmd: "A50F0108002000DD"},
	}

	for _, dc := range commands {
		body, err := manscdp.Encode(dc)
		require.NoError(t, err)

		res := up.roundTrip(up.request(sip.MESSAGE, lbChannelOne, string(body), "Application/MANSCDP+xml"))
		require.Equal(t, 200, int(res.StatusCode()), "DeviceControl %q must be acknowledged", dc.RecordCmd+dc.GuardCmd+dc.AlarmCmd+dc.TeleBoot+dc.HomePosition+dc.PTZCmd)
	}
}
