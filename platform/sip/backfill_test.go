package sip

// Coverage backfill across the SIP server's remaining surfaces: the
// SUBSCRIBE engine (wire round-trip + expiry resolution), the
// speculative-ACK/dialog-reset firmware-quirk helpers, playback INFO
// control beyond pause, the event bus unsubscribe path, the gosip log
// adapter surface, and address parsing helpers.

import (
	"context"
	"log/slog"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/ghettovoice/gosip/sip"
	"github.com/mickeyzzc/gb28181-go/manscdp"
	"github.com/mickeyzzc/gb28181-go/platform"
	"github.com/stretchr/testify/require"
)

// --- SUBSCRIBE engine ---

func TestSubscribeExpires(t *testing.T) {
	cases := map[string]time.Duration{
		"120s":    120 * time.Second,
		"1h":      time.Hour,
		"":        time.Hour, // default
		"garbage": time.Hour, // unparsable → default
		"-5s":     time.Hour, // negative → default
		"0s":      time.Hour, // zero → default
	}
	for in, want := range cases {
		s := &Server{cfg: Config{SubscribeExpires: in}}
		require.Equal(t, want, s.subscribeExpires(), "SubscribeExpires=%q", in)
	}
}

func TestSubscribeDeviceWireRoundTrip(t *testing.T) {
	cfg := testConfig(t)
	cfg.Enabled = true // default-on subjects (Catalog + Alarm)
	srv, dm := startTestServer(t, cfg)
	client := newSIPClient(t, cfg.SIPListen)

	dm.Register(&platform.Device{ID: testDeviceID, NetAddr: client.conn.LocalAddr().String()})

	srv.subscribeDevice(testDeviceID)

	// Fire-and-forget: the SUBSCRIBE lands on the device socket with the
	// Event header, Expires, and a MANSCDP Subscribe body.
	var sawCatalog, sawAlarm bool
	require.Eventually(t, func() bool {
		for {
			req := client.nextRequest(100 * time.Millisecond)
			if req == nil {
				break
			}
			if req.Method() != sip.SUBSCRIBE {
				continue
			}
			event := ""
			for _, hdr := range req.GetHeaders("Event") {
				event = hdr.String()
			}
			body := string(req.Body())
			if strings.Contains(event, "Catalog") && strings.Contains(body, "<CmdType>Catalog</CmdType>") {
				sawCatalog = true
				client.respondRaw(req, 200, "OK", "", "")
			}
			if strings.Contains(event, "Alarm") && strings.Contains(body, "<CmdType>Alarm</CmdType>") {
				sawAlarm = true
				client.respondRaw(req, 200, "OK", "", "")
			}
		}
		return sawCatalog && sawAlarm
	}, 10*time.Second, 50*time.Millisecond, "SUBSCRIBE Catalog+Alarm must reach the device")

	// The refresh deadline is recorded per subject.
	srv.subMu.Lock()
	n := len(srv.subscriptions)
	srv.subMu.Unlock()
	require.Equal(t, 2, n, "both subjects recorded")

	// Unsubscribe drops them again.
	srv.unsubscribeDevice(testDeviceID)
	srv.subMu.Lock()
	n = len(srv.subscriptions)
	srv.subMu.Unlock()
	require.Zero(t, n)
}

func TestSendSubscribeErrors(t *testing.T) {
	cfg := testConfig(t)
	srv, dm := startTestServer(t, cfg)

	// Unknown device.
	err := srv.sendSubscribe("34020000001329999999", manscdp.CmdCatalog)
	require.ErrorContains(t, err, "not registered")

	// Not-started server (fresh Server without Start).
	dm2 := platform.NewDeviceManager(time.Minute)
	dm2.Register(&platform.Device{ID: testDeviceID, NetAddr: "127.0.0.1:5060"})
	sm := platform.NewSessionManager(platform.NewPortManager(30300, 30310), cfg.ServerID)
	srv2 := NewServer(cfg, dm2, sm, nil)
	err = srv2.sendSubscribe(testDeviceID, manscdp.CmdCatalog)
	require.ErrorContains(t, err, "SIP server not started")

	// Malformed device address on an otherwise valid server.
	dm.Register(&platform.Device{ID: "34020000001320000099", NetAddr: "no-port-here"})
	err = srv.sendSubscribe("34020000001320000099", manscdp.CmdCatalog)
	require.ErrorContains(t, err, "invalid device address")

	_ = dm
}

// --- firmware-quirk helpers ---

func TestBuildSpeculativeAck(t *testing.T) {
	invite := buildRequest(t, sip.INVITE, testDeviceID, testServerID, "127.0.0.1:5060", 40000, "v=0\r\n")
	ack := buildSpeculativeAck(invite)

	require.Equal(t, sip.ACK, ack.Method())
	require.Equal(t, invite.Recipient().String(), ack.Recipient().String())

	// Same dialog identifiers.
	invCID, ok := invite.CallID()
	require.True(t, ok)
	ackCID, ok := ack.CallID()
	require.True(t, ok)
	require.Equal(t, invCID.Value(), ackCID.Value())

	ackCSeq, ok := ack.CSeq()
	require.True(t, ok)
	require.Equal(t, sip.ACK, ackCSeq.MethodName, "CSeq method must flip INVITE→ACK")
}

func TestSendDialogResetEmitsBye(t *testing.T) {
	cfg := testConfig(t)
	srv, _ := startTestServer(t, cfg)
	client := newSIPClient(t, cfg.SIPListen)

	// sendDialogReset is fire-and-forget; the BYE just has to reach the
	// device's socket. Poll in the test goroutine (a background answering
	// goroutine could outlive the test and t.Fatalf on the closed socket).
	srv.sendDialogReset(testDeviceID, fakeChannelID, client.conn.LocalAddr().String())

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if req := client.nextRequest(300 * time.Millisecond); req != nil && req.Method() == sip.BYE {
			return // BYE observed; unanswered retransmissions die with the socket
		}
	}

	t.Fatal("dialog-reset BYE never reached the device")
}

// --- playback INFO control (resume / seek / errors) ---

func TestPlaybackControlResumeSeek(t *testing.T) {
	cfg := testConfig(t)
	srv, dm := startTestServer(t, cfg)
	client := newSIPClient(t, cfg.SIPListen)

	sink := newFakeSink()
	enrol := &fakeEnroller{pbSink: sink}
	require.NoError(t, enrol.EnsureGB28181Camera(testDeviceID, fakeChannelID, "Front Door", "127.0.0.1"))
	srv.SetCameraEnroller(enrol)

	dm.Register(&platform.Device{ID: testDeviceID, NetAddr: client.conn.LocalAddr().String()})
	catalog, err := manscdp.Encode(manscdp.Catalog{
		CmdType: manscdp.CmdCatalog, SN: 1, DeviceID: testDeviceID, SumNum: 1,
		Item: []manscdp.Item{{DeviceID: fakeChannelID, Name: "Front Door", Parental: 0}},
	})
	require.NoError(t, err)
	client.sendMessage(string(catalog))
	require.Eventually(t, func() bool { return len(dm.Channels(testDeviceID)) == 1 },
		3*time.Second, 50*time.Millisecond, "catalog must register the channel")

	start := time.Now().Add(-2 * time.Hour)
	fetchDone := make(chan error, 1)
	go func() { fetchDone <- srv.StartPlayback(testDeviceID, fakeChannelID, start, start.Add(time.Hour)) }()

	err = client.answerRetransmits(sip.INVITE, func(invite sip.Request) {
		client.respondRaw(invite, 200, "OK", string(invite.Body()), "application/sdp")
	}, fetchDone, 15*time.Second)
	require.NoError(t, err)

	// Resume with a scale: PLAY MANSRTSP carrying Scale + Range.
	resumed := make(chan error, 1)
	go func() { resumed <- srv.PlaybackControl(fakeChannelID, "resume", 4, 10) }()
	err = client.answerRetransmits(sip.INFO, func(info sip.Request) {
		body := string(info.Body())
		require.Contains(t, body, "PLAY MANSRTSP")
		require.Contains(t, body, "Scale: 4.00")
		require.Contains(t, body, "Range: npt=10.000-")
		client.respondRaw(info, 200, "OK", "", "")
	}, resumed, 10*time.Second)
	require.NoError(t, err)

	st, ok := srv.PlaybackStatusFor(fakeChannelID)
	require.True(t, ok)
	require.False(t, st.Paused, "resume clears pause")

	// Seek clamps negative positions to zero.
	sought := make(chan error, 1)
	go func() { sought <- srv.PlaybackControl(fakeChannelID, "seek", 0, -5) }()
	err = client.answerRetransmits(sip.INFO, func(info sip.Request) {
		require.Contains(t, string(info.Body()), "Range: npt=0.000-")
		client.respondRaw(info, 200, "OK", "", "")
	}, sought, 10*time.Second)
	require.NoError(t, err)

	// Unknown action and unknown channel fail fast without wire traffic.
	require.ErrorContains(t, srv.PlaybackControl(fakeChannelID, "rewind", 0, 0), "unknown playback action")
	require.ErrorContains(t, srv.PlaybackControl("no-such-channel", "pause", 0, 0), "no active playback")

	require.NoError(t, srv.StopPlayback(fakeChannelID))
}

// --- event bus ---

func TestEventBusUnsubscribe(t *testing.T) {
	bus := NewEventBus(0) // default buffer
	ch := make(chan Event, 4)
	require.NoError(t, bus.Subscribe("gb28181.alarm", ch, 0))

	bus.Publish(context.Background(), "gb28181.alarm", "first")
	select {
	case evt := <-ch:
		require.Equal(t, "first", evt.Data)
	case <-time.After(time.Second):
		t.Fatal("publish before unsubscribe lost")
	}

	bus.Unsubscribe("gb28181.alarm", ch)
	bus.Publish(context.Background(), "gb28181.alarm", "dropped")

	select {
	case evt := <-ch:
		t.Fatalf("event after unsubscribe: %+v", evt)
	case <-time.After(100 * time.Millisecond):
	}
}

// --- gosip log adapter surface ---

func TestSlogLoggerSurface(t *testing.T) {
	// The adapter must not panic across its surface.
	l := SlogLogger(slog.Default())
	l.Print("p")
	l.Printf("p %d", 1)
	l.Info("i")
	l.Infof("i %d", 2)
	l.Warn("w")
	l.Warnf("w %d", 3)
	l.Error("e")
	l.Errorf("e %d", 4)
	l.Trace("t")
	l.Tracef("t %d", 5)
	l.Debug("d")
	l.Debugf("d %d", 6)
}

// --- address helpers ---

func TestParseSIPListenAndLocalIP(t *testing.T) {
	host, port, err := parseSIPListen("192.0.2.4:5060")
	require.NoError(t, err)
	require.Equal(t, "192.0.2.4", host)
	require.Equal(t, 5060, port)

	_, _, err = parseSIPListen("no-port")
	require.Error(t, err)

	_, _, err = parseSIPListen("192.0.2.4:not-a-port")
	require.Error(t, err)

	// localIP prefers the configured listen host.
	s := &Server{cfg: Config{SIPListen: "192.0.2.9:5060"}}
	require.Equal(t, "192.0.2.9", s.localIP())

	// 0.0.0.0 falls back to interface enumeration (a non-loopback IPv4
	// exists on every CI runner; loopback-only environments get 127.0.0.1).
	s = &Server{cfg: Config{SIPListen: "0.0.0.0:5060"}}
	ip := s.localIP()
	require.NotNil(t, net.ParseIP(ip), "localIP must return an IP, got %q", ip)
}

func TestTLSTransportParams(t *testing.T) {
	s := &Server{cfg: Config{SIPTransport: "tls"}}
	params := s.tlsTransportParams()
	v, ok := params.Get("transport")
	require.True(t, ok)
	require.Equal(t, "tls", v.String())

	s = &Server{cfg: Config{SIPTransport: "udp"}}
	require.Nil(t, s.tlsTransportParams(), "no transport param outside TLS mode")
}

// --- ByeAllSessions with a live session ---

func TestByeAllSessionsLiveSession(t *testing.T) {
	cfg := testConfig(t)
	srv, dm := startTestServer(t, cfg)
	client := newSIPClient(t, cfg.SIPListen)

	dm.Register(&platform.Device{ID: testDeviceID, NetAddr: client.conn.LocalAddr().String()})

	// Install a live session the blanket BYE must tear down. The teardown
	// BYE is not answered — the local session is gone the moment
	// sessionMgr.Bye runs; unanswered retransmissions die with the socket
	// (a background responder risks t.Fatalf-ing after test cleanup).
	ch := &platform.Channel{ID: fakeChannelID, DeviceID: testDeviceID}
	ch.Status.Store(platform.ChannelIdle)
	offer := []byte("v=0\r\no=- 0 0 IN IP4 192.168.1.100\r\nc=IN IP4 192.168.1.100\r\nm=video 0 RTP/AVP 96\r\n")
	_, err := srv.sessionMgr.Invite(ch, "127.0.0.1", client.conn.LocalAddr().String(), offer, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, srv.sessionMgr.GetReceiver(fakeChannelID))

	srv.ByeAllSessions()

	require.Eventually(t, func() bool {
		// Drain the socket so the BYE's send path is exercised end to end.
		_ = client.nextRequest(50 * time.Millisecond)
		return srv.sessionMgr.GetReceiver(fakeChannelID) == nil
	}, 5*time.Second, 50*time.Millisecond, "blanket BYE must tear the session down")
}
