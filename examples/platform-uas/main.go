// Platform-side (UAS) minimal example: the GB/T 28181 SIP server devices
// REGISTER to, with digest auth, keepalive liveness, catalog handling and
// INVITE-driven live streaming into a FrameHub fan-out. Persistence is a
// host seam (DeviceStore); this example uses an in-memory stub.
package main

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/mickeyzzc/gb28181-go/platform"
	gbsip "github.com/mickeyzzc/gb28181-go/platform/sip"
)

// memStore is an in-memory DeviceStore — the persistence seam. A real host
// writes through to its database here.
type memStore struct {
	mu       sync.Mutex
	devices  map[string]gbsip.GB28181Device
	channels map[string]gbsip.GB28181Channel
}

func newMemStore() *memStore {
	return &memStore{
		devices:  map[string]gbsip.GB28181Device{},
		channels: map[string]gbsip.GB28181Channel{},
	}
}

func (m *memStore) UpsertGB28181Device(_ context.Context, d gbsip.GB28181Device) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.devices[d.ID] = d
	return nil
}

func (m *memStore) UpsertGB28181Channel(_ context.Context, c gbsip.GB28181Channel) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.channels[c.ID] = c
	return nil
}

func (m *memStore) ListGB28181Devices(context.Context) ([]gbsip.GB28181Device, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]gbsip.GB28181Device, 0, len(m.devices))
	for _, d := range m.devices {
		out = append(out, d)
	}
	return out, nil
}

func (m *memStore) ListGB28181Channels(_ context.Context, deviceID string) ([]gbsip.GB28181Channel, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []gbsip.GB28181Channel
	for _, c := range m.channels {
		if c.DeviceID == deviceID {
			out = append(out, c)
		}
	}
	return out, nil
}

func (m *memStore) MarkDeviceOffline(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if d, ok := m.devices[id]; ok {
		d.Status = "offline"
		m.devices[id] = d
	}
	return nil
}

func (m *memStore) BindChannelCamera(_ context.Context, channelID, cameraID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if c, ok := m.channels[channelID]; ok {
		c.CameraID = cameraID
		m.channels[channelID] = c
	}
	return nil
}

func (m *memStore) DeleteGB28181Device(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.devices, id)
	return nil
}

func (m *memStore) GetGB28181Device(_ context.Context, id string) (*gbsip.GB28181Device, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if d, ok := m.devices[id]; ok {
		return &d, nil
	}
	return nil, fmt.Errorf("device %s not found", id)
}

func (m *memStore) DeleteGB28181Channel(_ context.Context, channelID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.channels, channelID)
	return nil
}

func main() {
	cfg := gbsip.Config{
		Enabled:        true,
		SIPListen:      ":5060",
		ServerID:       "34020000002000000001",
		Realm:          "3402000000",
		Password:       "12345678",
		PortRange:      "30000-30050",
		MediaTransport: "udp",
	}

	dm := platform.NewDeviceManager(60 * time.Second) // keepalive window
	bus := gbsip.NewEventBus(64)

	// Subscribe to device alarms (published on TopicGB28181Alarm).
	ch := make(chan gbsip.Event, 8)
	_ = bus.Subscribe(gbsip.TopicGB28181Alarm, ch, 0)
	go func() {
		for evt := range ch {
			fmt.Printf("alarm event: %+v\n", evt.Data)
		}
	}()

	// RTP port pool + session manager drive INVITE media sessions; the
	// server wires BYE/first-RTP hooks itself inside NewServer.
	ports := platform.NewPortManager(30000, 30050)
	sessions := platform.NewSessionManager(ports, cfg.ServerID)

	srv := gbsip.NewServer(cfg, dm, sessions, newMemStore())

	if err := srv.Start(context.Background()); err != nil {
		log.Fatalf("platform server: %v", err)
	}
	defer func() { _ = srv.Stop() }()

	// To pull a live stream once a device has registered and reported its
	// catalog, the host asks the server to INVITE the channel:
	//
	//   sdpAnswer, err := srv.InviteChannel(deviceID, channelID)
	//   hub := sessions.GetHub(channelID)  // demuxed AU fan-out
	//   hub.Subscribe("recorder", func(pts int64, au [][]byte) { ... })
	//   ...
	//   _ = srv.ByeChannel(deviceID, channelID)
	select {}
}
