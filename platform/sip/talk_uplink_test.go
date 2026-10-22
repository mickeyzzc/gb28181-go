package sip

// Uplink-leg tests for the § 9.4 intercom (#141): the sendrecv offer
// advertises the talk socket, devices with talkback upstream stream return
// PCMA to it, and the read loop must validate, count, and deliver — while
// garbage must neither wedge the session nor pollute the counters.

import (
	"encoding/binary"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// sendUplinkRTP pushes one return-audio packet from the fake device's
// media socket at the platform's advertised talk address.
func sendUplinkRTP(t *testing.T, media *net.UDPConn, talk *talkSession, ssrc uint32, seq uint16, fill byte) {
	t.Helper()
	pkt := make([]byte, 12+160)
	pkt[0] = 0x80
	pkt[1] = 8
	binary.BigEndian.PutUint16(pkt[2:], seq)
	binary.BigEndian.PutUint32(pkt[8:], ssrc)
	for i := 12; i < len(pkt); i++ {
		pkt[i] = fill
	}
	talkUpAddr := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: talk.conn.LocalAddr().(*net.UDPAddr).Port}
	_, err := media.WriteToUDP(pkt, talkUpAddr)
	require.NoError(t, err)
}

func TestTalkUplinkReceive(t *testing.T) {
	cfg := testConfig(t)
	srv, dm := startTestServer(t, cfg)
	client := newSIPClient(t, cfg.SIPListen)
	media := talkMediaSocket(t)

	// Wire before the session starts — StartTalk captures the hook once.
	var mu sync.Mutex
	var payloads [][]byte
	var channels []string
	srv.OnTalkUplink = func(channelID string, payload []byte) {
		mu.Lock()
		defer mu.Unlock()
		channels = append(channels, channelID)
		payloads = append(payloads, append([]byte(nil), payload...))
	}

	startTalkDialog(t, srv, dm, client, media)

	srv.talkMu.Lock()
	ts := srv.talks[fakeChannelID]
	srv.talkMu.Unlock()
	require.NotNil(t, ts, "talk session must exist after handshake")

	sendUplinkRTP(t, media, ts, 0x11111111, 1, 0xD5)
	sendUplinkRTP(t, media, ts, 0x11111111, 2, 0x55)
	sendUplinkRTP(t, media, ts, 0x22222222, 3, 0xD5) // SSRC change tolerated

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(payloads) == 3
	}, 5*time.Second, 10*time.Millisecond, "all uplink frames must be delivered")

	mu.Lock()
	require.Equal(t, []string{fakeChannelID, fakeChannelID, fakeChannelID}, channels)
	require.Len(t, payloads[0], 160)
	require.Equal(t, byte(0x55), payloads[1][0])
	mu.Unlock()

	st := srv.TalkStatusFor("cam-1")
	require.True(t, st.Active)
	require.Equal(t, int64(3), st.UplinkPackets)
	require.Equal(t, int64(3*160), st.UplinkBytes)
	require.Equal(t, uint32(0x22222222), st.UplinkSSRC, "last SSRC wins")

	// Garbage must be dropped without counting and without wedging the
	// loop — a valid frame still lands afterwards.
	_, err := media.WriteToUDP([]byte("not-rtp-at-all"), &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: ts.conn.LocalAddr().(*net.UDPAddr).Port})
	require.NoError(t, err)
	short := []byte{0x80, 0x08}
	_, err = media.WriteToUDP(short, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: ts.conn.LocalAddr().(*net.UDPAddr).Port})
	require.NoError(t, err)
	videoPT := make([]byte, 12+160)
	videoPT[0], videoPT[1] = 0x80, 96
	_, err = media.WriteToUDP(videoPT, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: ts.conn.LocalAddr().(*net.UDPAddr).Port})
	require.NoError(t, err)

	sendUplinkRTP(t, media, ts, 0x22222222, 4, 0x2A)
	require.Eventually(t, func() bool {
		return srv.TalkStatusFor("cam-1").UplinkPackets == 4
	}, 5*time.Second, 10*time.Millisecond, "loop survives garbage, valid frame counted")

	st = srv.TalkStatusFor("cam-1")
	require.Equal(t, int64(4*160), st.UplinkBytes)

	// Teardown closes the socket; the read loop exits without noise.
	require.NoError(t, srv.StopTalk(fakeChannelID))
	require.False(t, srv.TalkStatusFor("cam-1").Active)
}
