package platform

// Coverage backfill for the platform package's zero-coverage surfaces:
// FrameHub audio fan-out and counters, the catalog controller, the
// non-PTZ DeviceControl elements, PTZ preset/cruise sending, and the
// RTP receiver's accessor methods.

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// --- FrameHub: audio fan-out + counters ---

func TestFrameHubAudioFanOut(t *testing.T) {
	h := NewFrameHub()
	h.SetCameraID("cam-42")
	require.Equal(t, "cam-42", h.CameraID())

	var mu sync.Mutex
	got := map[string][]string{}

	require.NoError(t, h.SubscribeAudio("talk", func(pts int64, codec string, data []byte) {
		mu.Lock()
		defer mu.Unlock()
		got["talk"] = append(got["talk"], codec)
	}))
	require.NoError(t, h.SubscribeAudio("rec", func(pts int64, codec string, data []byte) {
		mu.Lock()
		defer mu.Unlock()
		got["rec"] = append(got["rec"], codec)
	}))

	// Duplicate audio subscription is rejected.
	err := h.SubscribeAudio("talk", func(int64, string, []byte) {})
	require.Error(t, err)
	require.Contains(t, err.Error(), "already subscribed")

	// Counters include both video and audio consumers.
	require.Equal(t, 2, h.ConsumerCount())

	h.BroadcastAudio(1000, "g711a", []byte{0x00, 0x01})
	h.BroadcastAudio(2000, "g711a", []byte{0x02})

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(got["talk"]) == 2 && len(got["rec"]) == 2
	}, 2*time.Second, 5*time.Millisecond, "audio frames must reach both consumers")

	// UnsubscribeAudio stops delivery; unknown IDs are a no-op.
	h.UnsubscribeAudio("talk")
	h.UnsubscribeAudio("never-existed")
	h.BroadcastAudio(3000, "g711a", []byte{0x03})

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(got["rec"]) == 3
	}, 2*time.Second, 5*time.Millisecond)

	mu.Lock()
	talkAfter := len(got["talk"])
	mu.Unlock()
	require.Equal(t, 2, talkAfter, "unsubscribed audio consumer must not receive more frames")
}

func TestFrameHubConsumerCountVideo(t *testing.T) {
	h := NewFrameHub()
	require.Equal(t, 0, h.ConsumerCount())

	require.NoError(t, h.Subscribe("video", func(int64, [][]byte, bool) {}))
	require.Equal(t, 1, h.ConsumerCount())

	h.Unsubscribe("video")
	require.Equal(t, 0, h.ConsumerCount())
}

// --- CatalogController ---

func TestCatalogControllerRequestCatalog(t *testing.T) {
	m := NewDeviceManager(time.Minute)
	dev := &Device{ID: "34020000001310000001", NetAddr: "192.168.1.50:5060"}
	m.Register(dev)
	m.RegisterChannel(dev.ID, &Channel{ID: "34020000001320000001"})

	sender := &fakeMessageSender{}
	c := NewCatalogController(m, sender)

	require.NoError(t, c.RequestCatalog("34020000001310000001"))
	require.Equal(t, "34020000001310000001", sender.deviceID)
	// § 9.3.1: child-element form, not attribute form.
	require.Contains(t, sender.body, "<CmdType>Catalog</CmdType>")
	require.Contains(t, sender.body, "<DeviceID>34020000001310000001</DeviceID>")

	// Unknown device and offline device are both rejected.
	require.ErrorIs(t, c.RequestCatalog("34020000001319999999"), ErrDeviceOffline)

	m.MarkOffline(dev.ID)
	err := c.RequestCatalog(dev.ID)
	require.ErrorIs(t, err, ErrDeviceOffline)
}

func TestCatalogControllerSenderError(t *testing.T) {
	m := NewDeviceManager(time.Minute)
	dev := &Device{ID: "34020000001310000001"}
	m.Register(dev)

	c := NewCatalogController(m, errSender{})
	err := c.RequestCatalog(dev.ID)
	require.Error(t, err)
	require.Contains(t, err.Error(), "send Catalog query")
}

// --- SendDeviceControl: non-PTZ control elements ---

func TestSendDeviceControlElements(t *testing.T) {
	c, sender, _ := newPTZTestEnv(t, 2)

	cases := []struct {
		element string
		value   string
	}{
		{"RecordCmd", "Record"},
		{"GuardCmd", GuardCmdSet},
		{"AlarmCmd", "ResetAlarm"},
		{"TeleBoot", "Reboot"},
		{"HomePosition", "Set"},
	}

	for _, tc := range cases {
		require.NoError(t, c.SendDeviceControl("34020000001320000001", tc.element, tc.value))
		require.Contains(t, sender.body, "<"+tc.element+">"+tc.value+"</"+tc.element+">", tc.element)
		require.Contains(t, sender.body, "<CmdType>DeviceControl</CmdType>")
	}

	err := c.SendDeviceControl("34020000001320000001", "BogusCmd", "x")
	require.Error(t, err)
	require.Contains(t, err.Error(), "unsupported DeviceControl element")
}

func TestSendDeviceControlOfflineAndMissing(t *testing.T) {
	c, _, m := newPTZTestEnv(t, 2)

	require.ErrorIs(t, c.SendDeviceControl("34020000001329999999", "RecordCmd", "Record"), ErrChannelNotFound)

	m.MarkOffline("34020000001310000001")
	require.ErrorIs(t, c.SendDeviceControl("34020000001320000001", "RecordCmd", "Record"), ErrDeviceOffline)
}

// --- PTZ preset / cruise send paths ---

func TestSendPTZPresetPaths(t *testing.T) {
	c, sender, _ := newPTZTestEnv(t, 2)

	require.NoError(t, c.SendPTZPreset("34020000001320000001", PresetSet, 5))
	require.Contains(t, sender.body, "A50F01810000053B")

	err := c.SendPTZPreset("34020000001329999999", PresetSet, 5)
	require.ErrorIs(t, err, ErrChannelNotFound)

	err = c.SendPTZPreset("34020000001320000001", "bogus", 5)
	require.Error(t, err)
}

func TestSendPTZPresetUnsupported(t *testing.T) {
	c, _, _ := newPTZTestEnv(t, 0)
	err := c.SendPTZPreset("34020000001320000001", PresetSet, 5)
	require.ErrorIs(t, err, ErrPTZUnsupported)
}

func TestSendPTZCruisePaths(t *testing.T) {
	c, sender, _ := newPTZTestEnv(t, 2)

	require.NoError(t, c.SendPTZCruise("34020000001320000001", CruiseAddPoint, 1, 5))
	require.Contains(t, sender.body, "A50F")

	// CruiseStop maps to a plain PTZ stop command.
	require.NoError(t, c.SendPTZCruise("34020000001320000001", CruiseStop, 1, 0))
	require.Contains(t, sender.body, "A50F0100")

	err := c.SendPTZCruise("34020000001329999999", CruiseAddPoint, 1, 5)
	require.ErrorIs(t, err, ErrChannelNotFound)

	err = c.SendPTZCruise("34020000001320000001", "bogus", 1, 5)
	require.Error(t, err)
}

// --- Receiver accessors ---

func TestReceiverAccessors(t *testing.T) {
	r := NewReceiver("cam-1", NewFrameHub(), NewPortManager(30000, 30100))

	require.False(t, r.HasReceivedRTP())
	require.Zero(t, r.SinceLastPacket())

	_, gotIDR := r.SinceLastIDR()
	require.False(t, gotIDR, "no IDR before any packet")

	// The hint seeds the demuxer only; it must not fake packet arrival.
	r.SetAudioCodecHint("g711a")
	require.False(t, r.HasReceivedRTP())
	require.False(t, r.Running())
}
