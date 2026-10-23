package platform

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pion/rtp"
	"github.com/stretchr/testify/require"
)

// startUDPReceiver spins a receiver over a local UDP pair and returns the
// client side. onAudio (optional) installs the AudioCallback BEFORE Start —
// the field is read on the receiver goroutine and must not be assigned
// after the read loop is live (CI -race contract).
func startUDPReceiver(t *testing.T, cameraID string, audioOnly bool, onAudio func(AudioFrame)) (*Receiver, net.Conn) {
	t.Helper()
	hub := NewFrameHub()
	pm := NewPortManager(20100, 20110)
	rec := NewReceiver(cameraID, hub, pm)
	rec.SetAudioOnly(audioOnly)
	if onAudio != nil {
		rec.AudioCallback = onAudio
	}

	serverConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	require.NoError(t, err)
	clientConn, err := net.DialUDP("udp", nil, serverConn.LocalAddr().(*net.UDPAddr))
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	require.NoError(t, rec.Start(ctx, serverConn))
	t.Cleanup(func() {
		cancel()
		_ = rec.Stop()
		_ = clientConn.Close()
	})
	return rec, clientConn
}

// feedLoop writes one RTP packet every interval until stop closes. seq and
// timestamp advance by the given steps.
func feedLoop(t *testing.T, conn net.Conn, payload []byte, pt uint8, interval time.Duration) chan struct{} {
	t.Helper()
	stop := make(chan struct{})
	go func() {
		var seq uint16
		var ts uint32
		for {
			select {
			case <-stop:
				return
			default:
			}
			pkt := &rtp.Packet{Header: rtp.Header{
				SequenceNumber: seq, Timestamp: ts, Marker: true, PayloadType: pt,
			}, Payload: payload}
			buf, err := pkt.Marshal()
			if err != nil {
				return
			}
			if _, err := conn.Write(buf); err != nil {
				return
			}
			seq++
			ts += uint32(len(payload))
			time.Sleep(interval)
		}
	}()
	t.Cleanup(func() { close(stop) })
	return stop
}

// TestReceiverStalledSignature: RTP keeps arriving while the demuxer
// produces nothing (incomplete PS pack headers never complete a pack) — the
// receiver must report Stalled. A healthy bare-audio receiver receiving the
// same traffic rate must NOT report Stalled.
func TestReceiverStalledSignature(t *testing.T) {
	stallFor := 250 * time.Millisecond

	stalled, c1 := startUDPReceiver(t, "cam-stalled", false, nil)
	// 9-byte fragment: PS pack start code but shorter than the 14-byte pack
	// header — the PS demuxer buffers it forever, emitting nothing.
	frag := []byte{0x00, 0x00, 0x01, 0xBA, 0, 0, 0, 0, 0}
	feedLoop(t, c1, frag, 96, 10*time.Millisecond)

	var emitted atomic.Int64
	healthy, c2 := startUDPReceiver(t, "cam-healthy", true, func(AudioFrame) { emitted.Add(1) })
	silence := make([]byte, 160)
	for i := range silence {
		silence[i] = 0x55
	}
	feedLoop(t, c2, silence, 8, 10*time.Millisecond)

	// (emitted counter + callback installed pre-Start inside startUDPReceiver)

	require.Eventually(t, func() bool {
		return stalled.Metrics()["packets_received"] > 20
	}, 2*time.Second, 20*time.Millisecond, "traffic must arrive")

	require.False(t, stalled.Classification() == "", "classification latches on the first payload")
	require.Equal(t, "ps", stalled.Classification())
	require.Eventually(t, func() bool {
		return stalled.Stalled(stallFor)
	}, 2*time.Second, 50*time.Millisecond, "packets arriving + zero output must read as stalled")

	require.Eventually(t, func() bool {
		return emitted.Load() > 10
	}, 2*time.Second, 20*time.Millisecond, "healthy receiver emits")
	require.False(t, healthy.Stalled(stallFor), "an emitting receiver is never stalled")
	require.Zero(t, healthy.Metrics()["au_emitted"], "bare audio never uses the AU path")
	require.Greater(t, healthy.Metrics()["audio_frames_emitted"], int64(10))
	require.Equal(t, "bare-audio", healthy.Classification())
}

// TestSessionStallWatchdog: the supervisor recycles a wedged session (BYE
// ordering via the registered sender, teardown, recycle counter) and leaves
// healthy sessions alone.
func TestSessionStallWatchdog(t *testing.T) {
	pm := NewPortManager(20110, 20120)
	sm := NewSessionManager(pm, "34020000001320000001")

	var byes atomic.Int64
	sm.SetByeSender(func(string) error { byes.Add(1); return nil })

	// Wedged receiver: incomplete PS fragments, never emitting.
	rcv, client := startUDPReceiver(t, "cam-wedged", false, nil)
	frag := []byte{0x00, 0x00, 0x01, 0xBA, 0, 0, 0, 0, 0}
	feedLoop(t, client, frag, 96, 10*time.Millisecond)

	// Healthy receiver: bare audio emitting normally.
	healthy, client2 := startUDPReceiver(t, "cam-fine", true, func(AudioFrame) {})
	silence := make([]byte, 160)
	for i := range silence {
		silence[i] = 0x55
	}
	feedLoop(t, client2, silence, 8, 10*time.Millisecond)

	p1, err := pm.Get()
	require.NoError(t, err)
	p2, err := pm.Get()
	require.NoError(t, err)
	sm.mu.Lock()
	sm.sessions["cam-wedged"] = &session{channelID: "cam-wedged", receiver: rcv, port: p1}
	sm.sessions["cam-fine"] = &session{channelID: "cam-fine", receiver: healthy, port: p2}
	sm.mu.Unlock()
	t.Cleanup(sm.StopAll)

	var recycled atomic.Int64
	sm.SetStallWatchdog(30*time.Millisecond, 300*time.Millisecond, func(id string) {
		if id == "cam-wedged" {
			recycled.Add(1)
		}
	})

	require.Eventually(t, func() bool {
		return sm.StallRecycles() == 1 && sm.GetReceiver("cam-wedged") == nil
	}, 4*time.Second, 30*time.Millisecond, "wedged session must be recycled")
	require.EqualValues(t, 1, byes.Load(), "the recycle path sends a SIP BYE")
	require.EqualValues(t, 1, recycled.Load(), "onRecycle hook fires for the wedged channel")
	require.NotNil(t, sm.GetReceiver("cam-fine"), "healthy session survives the watchdog")

	// The status snapshot reflects the survivor.
	sts := sm.Statuses()
	require.Len(t, sts, 1)
	require.Equal(t, "cam-fine", sts[0].ChannelID)
	require.Equal(t, "bare-audio", sts[0].Classification)
	require.False(t, sts[0].Stalled)
}
