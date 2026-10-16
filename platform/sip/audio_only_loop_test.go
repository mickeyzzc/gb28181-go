package sip

// Audio-only channel loopback (issue #135, MiBeeNvr #962 prerequisite): a
// voice-input channel (Table E.1 type code 136, 网络拾音器) enrolls like a
// camera channel, but the platform's live INVITE carries a lone m=audio
// line (G.711 A-law, PT 8) and the device answers bare G.711 RTP. Video
// channels keep the m=video form — see the live loop suite.

import (
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ghettovoice/gosip/sip"
	"github.com/mickeyzzc/gb28181-go/manscdp"
	"github.com/mickeyzzc/gb28181-go/platform"
	"github.com/pion/rtp"
	"github.com/stretchr/testify/require"
)

// audioChannelID is a 20-digit ID with the Table E.1 type code 136
// (语音输入设备) in digits 11-13.
const audioChannelID = "34020000001360000001"

func TestChannelTypeCodeParsing(t *testing.T) {
	require.Equal(t, 136, platform.ChannelTypeCode(audioChannelID))
	require.Equal(t, 132, platform.ChannelTypeCode("34020000001320000001"))
	require.Equal(t, 131, platform.ChannelTypeCode("34020000001310000001"))
	require.Equal(t, 0, platform.ChannelTypeCode("short"))
	require.Equal(t, 0, platform.ChannelTypeCode("3402000000abc0000001"))
	require.True(t, platform.IsAudioInputChannel(audioChannelID))
	require.False(t, platform.IsAudioInputChannel(fakeChannelID))
}

func TestAudioOnlyChannelInviteAndBareG711(t *testing.T) {
	cfg := testConfig(t)
	srv, dm := startTestServer(t, cfg)
	client := newSIPClient(t, cfg.SIPListen)

	var frames atomic.Int64
	var mu sync.Mutex
	gotCodec := ""
	enrol := &fakeEnroller{audioWriter: func(codec string, data []byte, cfg []byte, ptsTicks int64, samples int) {
		frames.Add(1)
		mu.Lock()
		gotCodec = codec
		mu.Unlock()
	}}
	require.NoError(t, enrol.EnsureGB28181Camera(testDeviceID, audioChannelID, "Hall Pickup", "127.0.0.1"))
	srv.SetCameraEnroller(enrol)

	dm.Register(&platform.Device{ID: testDeviceID, NetAddr: client.conn.LocalAddr().String()})
	catalog, err := manscdp.Encode(manscdp.Catalog{
		CmdType: manscdp.CmdCatalog, SN: 1, DeviceID: testDeviceID, SumNum: 1,
		Item: []manscdp.Item{{DeviceID: audioChannelID, Name: "Hall Pickup", Parental: 0}},
	})
	require.NoError(t, err)
	client.sendMessage(string(catalog))

	var mediaPort int
	require.Eventually(t, func() bool {
		req := client.nextRequest(150 * time.Millisecond)
		if req != nil && req.Method() == sip.INVITE {
			body := string(req.Body())
			require.Contains(t, body, "m=audio", "audio-only INVITE carries m=audio")
			require.NotContains(t, body, "m=video", "audio-only INVITE must not carry a video track")
			require.Contains(t, body, "a=rtpmap:8 PCMA/8000")
			if mediaPort == 0 {
				mediaPort = sdpAudioMediaPort(t, body)
			}
			client.respondRaw(req, 200, "OK", body, "application/sdp")
		}
		srv.mu.Lock()
		dlg := srv.dialogs[audioChannelID]
		srv.mu.Unlock()
		return dlg != nil && mediaPort != 0
	}, 10*time.Second, 10*time.Millisecond, "audio-only auto-INVITE must complete")
	require.NotZero(t, mediaPort)

	// Bare G.711 A-law RTP (PT 8) — the audio pickup's wire form.
	media, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	require.NoError(t, err)
	defer media.Close()
	dst := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: mediaPort}
	pkt := &rtp.Packet{Header: rtp.Header{PayloadType: 8, SequenceNumber: 1, Timestamp: 160, SSRC: 1234567, Marker: true}}
	payload := make([]byte, 160) // 20ms of 8kHz G.711
	for i := range payload {
		payload[i] = 0xD5 // idle A-law code
	}
	pkt.Payload = payload
	raw, err := pkt.Marshal()
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		if _, err := media.WriteToUDP(raw, dst); err != nil {
			return false
		}
		pkt.Header.SequenceNumber++
		pkt.Header.Timestamp += 160
		raw, _ = pkt.Marshal()
		mu.Lock()
		defer mu.Unlock()
		return frames.Load() >= 2 && gotCodec == "g711a"
	}, 5*time.Second, 50*time.Millisecond, "bare G.711 frames must reach the audio writer")

	byed := make(chan error, 1)
	go func() { byed <- srv.ByeChannelByID(audioChannelID) }()
	require.NoError(t, client.answerRetransmits(sip.BYE, func(bye sip.Request) {
		client.respondRaw(bye, 200, "OK", "", "")
	}, byed, 10*time.Second))
	_ = srv.Stop()
}

// TestAudioOnlyChannelRegisterDerivation pins the RegisterChannel
// derivation: the type code and AudioOnly flag come from the ID, no
// catalog field involved.
func TestAudioOnlyChannelRegisterDerivation(t *testing.T) {
	dm := platform.NewDeviceManager(time.Minute)
	dm.Register(&platform.Device{ID: testDeviceID})
	dm.RegisterChannel(testDeviceID, &platform.Channel{ID: audioChannelID, Name: "pickup"})
	ch, ok := dm.FindChannel(testDeviceID, audioChannelID)
	require.True(t, ok)
	require.Equal(t, 136, ch.TypeCode)
	require.True(t, ch.AudioOnly)

	dm.RegisterChannel(testDeviceID, &platform.Channel{ID: fakeChannelID})
	ch2, ok := dm.FindChannel(testDeviceID, fakeChannelID)
	require.True(t, ok)
	require.Equal(t, 132, ch2.TypeCode)
	require.False(t, ch2.AudioOnly)
}

// sdpAudioMediaPort extracts the m=audio media port from an SDP body.
func sdpAudioMediaPort(t *testing.T, sdp string) int {
	t.Helper()
	for _, line := range strings.Split(sdp, "\r\n") {
		if strings.HasPrefix(line, "m=audio ") {
			port, err := strconv.Atoi(strings.Fields(line)[1])
			require.NoError(t, err)
			return port
		}
	}
	t.Fatalf("no m=audio line in SDP: %q", sdp)
	return 0
}
