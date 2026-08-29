package sip

// Voice-intercom (§ 9.4) loopback tests: StartTalk's audio-only INVITE →
// 200 OK handshake, RTP/PCMA delivery via WriteTalkAudio, status
// reporting, teardown via StopTalk (platform BYE) and via the device's
// BYE (stopTalkOnBye), plus the pure SDP audio helpers.

import (
	"encoding/binary"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ghettovoice/gosip/sip"
	"github.com/mickeyzzc/gb28181-go/platform"
	"github.com/stretchr/testify/require"
)

// talkMediaSocket is the device-side audio receiver: the 200 OK points
// the platform's RTP stream at it.
func talkMediaSocket(t *testing.T) *net.UDPConn {
	t.Helper()

	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	return conn
}

// startTalkDialog drives StartTalk against a fake device that answers
// the audio INVITE with a 200 OK carrying the media socket's address.
func startTalkDialog(t *testing.T, srv *Server, dm *platform.DeviceManager, client *sipClient, media *net.UDPConn) {
	t.Helper()

	dm.Register(&platform.Device{ID: testDeviceID, NetAddr: client.conn.LocalAddr().String()})
	dev, ok := dm.Device(testDeviceID)
	require.True(t, ok)
	dev.Status.Store(platform.DeviceOnline)

	answerSDP := "v=0\r\n" +
		"o=" + fakeChannelID + " 0 0 IN IP4 127.0.0.1\r\n" +
		"s=Play\r\nc=IN IP4 127.0.0.1\r\nt=0 0\r\n" +
		"m=audio " + strconv.Itoa(media.LocalAddr().(*net.UDPAddr).Port) + " RTP/AVP 8\r\n" +
		"a=sendrecv\r\n"

	done := make(chan error, 1)
	go func() { done <- srv.StartTalk("cam-1", testDeviceID, fakeChannelID) }()

	require.Eventually(t, func() bool {
		req := client.nextRequest(150 * time.Millisecond)
		if req != nil && req.Method() == sip.INVITE {
			require.Contains(t, string(req.Body()), "m=audio", "talk INVITE is audio-only")
			require.Contains(t, string(req.Body()), "a=sendrecv", "talk INVITE is sendrecv")
			client.respondRaw(req, 200, "OK", answerSDP, "application/sdp")
		}
		select {
		case err := <-done:
			return err == nil
		default:
			return false
		}
	}, 10*time.Second, 10*time.Millisecond, "talk INVITE handshake must complete")
}

func TestTalkLoopbackStartWriteStop(t *testing.T) {
	cfg := testConfig(t)
	srv, dm := startTestServer(t, cfg)
	client := newSIPClient(t, cfg.SIPListen)
	media := talkMediaSocket(t)

	startTalkDialog(t, srv, dm, client, media)

	st := srv.TalkStatusFor("cam-1")
	require.True(t, st.Active, "talk must be established")
	require.Equal(t, fakeChannelID, st.ChannelID)
	require.Equal(t, int64(0), st.Packets)

	// Empty and oversized frames are silently dropped without killing
	// the session — the writes below still deliver afterwards.
	srv.WriteTalkAudio(fakeChannelID, nil)
	srv.WriteTalkAudio(fakeChannelID, make([]byte, 9000))

	// Unknown channel writes are a no-op.
	srv.WriteTalkAudio("no-such-channel", []byte{0xD5})

	// One G.711A frame (160 bytes = 20ms) must arrive as RTP/PCMA with
	// SSRC and incrementing sequence.
	alaw := make([]byte, 160)
	for i := range alaw {
		alaw[i] = byte(0xD5)
	}
	srv.WriteTalkAudio(fakeChannelID, alaw)
	srv.WriteTalkAudio(fakeChannelID, alaw)

	// Both frames must arrive, sequence numbers incrementing.
	require.NoError(t, media.SetReadDeadline(time.Now().Add(5*time.Second)))
	buf := make([]byte, 1500)
	var seqs []uint16
	for range 2 {
		n, _, err := media.ReadFromUDP(buf)
		require.NoError(t, err, "RTP audio must reach the device address")
		pkt := buf[:n]

		require.GreaterOrEqual(t, n, 12+160)
		require.Equal(t, byte(0x80), pkt[0], "RTP version 2")
		require.Equal(t, byte(8), pkt[1]&0x7F, "payload type PCMA")
		require.Equal(t, byte(0xD5), pkt[12], "G.711A payload first byte")
		require.NotZero(t, binary.BigEndian.Uint32(pkt[8:12]), "SSRC must be set")
		seqs = append(seqs, binary.BigEndian.Uint16(pkt[2:4]))
	}
	require.Equal(t, seqs[0]+1, seqs[1], "RTP sequence must increment per frame")

	st = srv.TalkStatusFor("cam-1")
	require.True(t, st.Active)
	require.Equal(t, int64(2), st.Packets, "both frames delivered")
	require.Equal(t, int64(320), st.BytesSent)

	// Platform-initiated stop: in-dialog BYE answered 200.
	byed := make(chan error, 1)
	go func() { byed <- srv.StopTalk(fakeChannelID) }()
	require.NoError(t, client.answerRetransmits(sip.BYE, func(bye sip.Request) {
		client.respondRaw(bye, 200, "OK", "", "")
	}, byed, 10*time.Second))

	// After stop the status is idle and stopping again is a no-op.
	require.False(t, srv.TalkStatusFor("cam-1").Active)
	require.NoError(t, srv.StopTalk(fakeChannelID))

	// Writes after stop are dropped silently.
	srv.WriteTalkAudio(fakeChannelID, alaw)
}

func TestTalkDeviceByeTearsDown(t *testing.T) {
	cfg := testConfig(t)
	srv, dm := startTestServer(t, cfg)
	client := newSIPClient(t, cfg.SIPListen)
	media := talkMediaSocket(t)

	startTalkDialog(t, srv, dm, client, media)
	require.True(t, srv.TalkStatusFor("cam-1").Active)

	// Build a device-side BYE on the talk's Call-ID (the wire handler
	// dispatches to the same matcher; driving it directly keeps the test
	// independent of response routing).
	srv.talkMu.Lock()
	talk := srv.talks[fakeChannelID]
	srv.talkMu.Unlock()
	require.NotNil(t, talk)
	require.NotEmpty(t, talk.callID)

	// gosip renders CallID.String() as the whole header line; the stored
	// talk.callID carries that rendering — strip it to the raw value.
	rawCallID := strings.TrimPrefix(talk.callID, "Call-ID: ")

	from := &sip.Address{
		DisplayName: sip.String{Str: fakeChannelID},
		Uri:         &sip.SipUri{FUser: sip.String{Str: fakeChannelID}, FHost: "127.0.0.1"},
	}
	to := &sip.Address{
		DisplayName: sip.String{Str: testServerID},
		Uri:         &sip.SipUri{FUser: sip.String{Str: testServerID}, FHost: "127.0.0.1"},
	}
	cidVal := sip.CallID(rawCallID)
	rb := sip.NewRequestBuilder().
		SetMethod(sip.BYE).
		SetFrom(from).
		SetTo(to).
		SetRecipient(to.Uri).
		SetHost("127.0.0.1").
		SetSeqNo(2).
		SetCallID(&cidVal).
		AddVia(&sip.ViaHop{Host: "127.0.0.1", Params: sip.NewParams().Add("branch", sip.String{Str: sip.GenerateBranch()})})
	byeReq, err := rb.Build()
	require.NoError(t, err)

	require.True(t, srv.stopTalkOnBye(byeReq), "BYE on the talk dialog must match")
	require.False(t, srv.TalkStatusFor("cam-1").Active, "talk must be down after device BYE")

	// A second BYE (or a foreign Call-ID) no longer matches anything.
	otherCID := sip.CallID("foreign-call-id")
	rb2 := sip.NewRequestBuilder().
		SetMethod(sip.BYE).
		SetFrom(from).
		SetTo(to).
		SetRecipient(to.Uri).
		SetHost("127.0.0.1").
		SetSeqNo(3).
		SetCallID(&otherCID).
		AddVia(&sip.ViaHop{Host: "127.0.0.1", Params: sip.NewParams().Add("branch", sip.String{Str: sip.GenerateBranch()})})
	otherReq, err := rb2.Build()
	require.NoError(t, err)
	require.False(t, srv.stopTalkOnBye(otherReq), "foreign Call-ID must not match")
}

func TestTalkStartErrors(t *testing.T) {
	// Unknown device on a started server: instant error.
	cfg := testConfig(t)
	srv, _ := startTestServer(t, cfg)

	err := srv.StartTalk("cam-1", "34020000001329999999", fakeChannelID)
	require.ErrorContains(t, err, "not registered")

	// Known device but the SIP server never started: instant error, no
	// network wait.
	dm2 := platform.NewDeviceManager(60 * time.Second)
	dm2.Register(&platform.Device{ID: testDeviceID, NetAddr: "127.0.0.1:5060"})
	sm := platform.NewSessionManager(platform.NewPortManager(30200, 30210), cfg.ServerID)
	srv2 := NewServer(cfg, dm2, sm, nil)

	err = srv2.StartTalk("cam-1", testDeviceID, fakeChannelID)
	require.ErrorContains(t, err, "SIP server not started")
}

func TestSDPAudioHelpers(t *testing.T) {
	host, port, ok := sdpAudioAddress([]byte("v=0\r\nc=IN IP4 192.0.2.7\r\nm=audio 4000 RTP/AVP 8\r\n"))
	require.True(t, ok)
	require.Equal(t, "192.0.2.7", host)
	require.Equal(t, uint16(4000), port)

	// Video-form SDP carries no usable audio address.
	_, _, ok = sdpAudioAddress([]byte("v=0\r\nc=IN IP4 192.0.2.7\r\nm=video 4000 RTP/AVP 96\r\n"))
	require.False(t, ok)

	// Garbage host / port out of range.
	_, _, ok = sdpAudioAddress([]byte("c=IN IP4 not-an-ip\r\nm=audio 4000 RTP/AVP 8\r\n"))
	require.False(t, ok)
	_, _, ok = sdpAudioAddress([]byte("c=IN IP4 192.0.2.7\r\nm=audio 70000 RTP/AVP 8\r\n"))
	require.False(t, ok)

	require.Equal(t, platform.AudioCodecG711A, sdpAudioCodec([]byte("m=audio 4000 RTP/AVP 8\r\na=rtpmap:8 PCMA/8000\r\n")))
	require.Equal(t, platform.AudioCodecG711U, sdpAudioCodec([]byte("m=audio 4000 RTP/AVP 0\r\na=rtpmap:0 PCMU/8000\r\n")))
	require.Equal(t, platform.AudioCodecAAC, sdpAudioCodec([]byte("m=audio 4000 RTP/AVP 96\r\na=rtpmap:96 MPEG4-GENERIC/48000/2\r\n")))

	// Codec declared on the video line must not leak into audio.
	require.Equal(t, "", sdpAudioCodec([]byte("m=video 4000 RTP/AVP 96\r\na=rtpmap:96 PCMA/90000\r\n")))
	require.Equal(t, "", sdpAudioCodec([]byte("m=audio 4000 RTP/AVP 8\r\na=rtpmap:8 CLEAR/8000\r\n")))
}
