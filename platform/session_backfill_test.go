package platform

// Coverage backfill for the session-manager seams: the SIP BYE sender
// wired from the SIP server, the once-per-session first-RTP hook, and
// ByeDevice bulk teardown on device offline.

import (
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func udpInviteSession(t *testing.T, pm *PortManager, sm *SessionManager, channelID, deviceID string) (*Channel, []byte) {
	t.Helper()

	ch := &Channel{ID: channelID, DeviceID: deviceID}
	ch.Status.Store(ChannelIdle)

	sdpOffer := []byte("v=0\r\no=- 0 0 IN IP4 192.168.1.100\r\nc=IN IP4 192.168.1.100\r\nm=video 0 RTP/AVP 96\r\n")
	sdpAnswer, err := sm.Invite(ch, "127.0.0.1", "192.168.1.100:5060", sdpOffer, nil, nil)
	require.NoError(t, err)

	return ch, sdpAnswer
}

func TestSessionManager_ByeSenderFiresOnBye(t *testing.T) {
	pm := NewPortManager(25300, 25310)
	sm := NewSessionManager(pm, "34020000002000000001")

	var mu sync.Mutex
	var byes []string

	sm.SetByeSender(func(channelID string) error {
		mu.Lock()
		defer mu.Unlock()
		byes = append(byes, channelID)
		return nil
	})

	ch, _ := udpInviteSession(t, pm, sm, "34020000001320000001", "34020000001310000001")
	require.NoError(t, sm.Bye(ch.ID))

	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, []string{ch.ID}, byes, "wired BYE sender must fire exactly once on Bye")
}

func TestSessionManager_ByeSenderFiresOnSessionReplace(t *testing.T) {
	pm := NewPortManager(25320, 25330)
	sm := NewSessionManager(pm, "34020000002000000001")

	var mu sync.Mutex
	var byes []string

	sm.SetByeSender(func(channelID string) error {
		mu.Lock()
		defer mu.Unlock()
		byes = append(byes, channelID)
		return nil
	})

	// First session, then a second Invite on the same channel replaces it.
	udpInviteSession(t, pm, sm, "34020000001320000001", "34020000001310000001")
	udpInviteSession(t, pm, sm, "34020000001320000001", "34020000001310000001")

	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, []string{"34020000001320000001"}, byes, "replaced session must BYE the device first")
}

func TestSessionManager_FirstRTPHook(t *testing.T) {
	pm := NewPortManager(25340, 25350)
	sm := NewSessionManager(pm, "34020000002000000001")

	var mu sync.Mutex
	var hookCalls []string

	sm.SetFirstRTPHook(func(channelID string) {
		mu.Lock()
		defer mu.Unlock()
		hookCalls = append(hookCalls, channelID)
	})

	ch, sdp := udpInviteSession(t, pm, sm, "34020000001320000001", "34020000001310000001")
	port := sdpMediaPort(t, sdp)

	// Send two RTP packets carrying a tiny PS AU: the hook must fire
	// exactly once (first packet only).
	ps := buildPS([][]byte{{0x67, 0x42, 0x00, 0x1F}}, 0x1B)
	conn, err := net.Dial("udp4", fmt.Sprintf("127.0.0.1:%d", port))
	require.NoError(t, err)
	defer conn.Close()

	for i := range 2 {
		_, err = conn.Write(buildRTPPacket(t, ps, 1000, uint16(i), true))
		require.NoError(t, err)
		time.Sleep(20 * time.Millisecond)
	}

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(hookCalls) > 0
	}, 5*time.Second, 50*time.Millisecond, "first-RTP hook must fire")

	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, []string{ch.ID}, hookCalls, "hook must fire once for the first packet only")
}

func TestSessionManager_ByeDevice(t *testing.T) {
	pm := NewPortManager(25360, 25380)
	sm := NewSessionManager(pm, "34020000002000000001")

	devA := "34020000001310000001"
	devB := "34020000001310000002"

	chA1, _ := udpInviteSession(t, pm, sm, "34020000001320000001", devA)
	chA2, _ := udpInviteSession(t, pm, sm, "34020000001320000002", devA)
	chB, _ := udpInviteSession(t, pm, sm, "34020000001320000003", devB)

	sm.ByeDevice(devA)

	// Device A's sessions are gone and reset to idle; B survives.
	require.Nil(t, sm.GetReceiver(chA1.ID))
	require.Nil(t, sm.GetReceiver(chA2.ID))
	require.NotNil(t, sm.GetReceiver(chB.ID))

	require.Equal(t, ChannelIdle, chA1.Status.Load())
	require.Equal(t, ChannelIdle, chA2.Status.Load())

	require.NoError(t, sm.Bye(chB.ID))
}
