package main

// platform-uas: the minimal platform-side (UAS) server — the same sample
// that lived at examples/platform-uas, with the hardcoded configuration
// promoted to flags.

import (
	"context"
	"flag"
	"fmt"
	"log"
	"strconv"
	"strings"
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

func runPlatformUAS(args []string) {
	fs := flag.NewFlagSet("platform-uas", flag.ExitOnError)
	listen := fs.String("listen", ":5060", "SIP listen address")
	serverID := fs.String("server-id", "34020000002000000001", "20-digit platform (server) ID")
	realm := fs.String("realm", "3402000000", "digest realm (usually the domain)")
	password := fs.String("password", "", "digest-auth secret devices must present (required)")
	mediaPorts := fs.String("media-ports", "30000-30050", "RTP media port range lo-hi")
	keepalive := fs.Uint("keepalive-window", 60, "device liveness window, seconds")
	_ = fs.Parse(args)

	if *password == "" {
		log.Fatal("platform-uas: -password is required (the digest secret devices will use)")
	}
	parts := strings.SplitN(*mediaPorts, "-", 2)
	lo, err := strconv.Atoi(parts[0])
	if err != nil {
		log.Fatalf("platform-uas: -media-ports must be lo-hi, got %q: %v", *mediaPorts, err)
	}
	hi := lo
	if len(parts) == 2 {
		if hi, err = strconv.Atoi(parts[1]); err != nil || hi < lo {
			log.Fatalf("platform-uas: -media-ports hi must be numeric ≥ lo, got %q: %v", *mediaPorts, err)
		}
	}

	cfg := gbsip.Config{
		Enabled:        true,
		SIPListen:      *listen,
		ServerID:       *serverID,
		Realm:          *realm,
		Password:       *password,
		PortRange:      fmt.Sprintf("%d-%d", lo, hi),
		MediaTransport: "udp",
	}

	dm := platform.NewDeviceManager(time.Duration(*keepalive) * time.Second) // keepalive window
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
	ports := platform.NewPortManager(uint16(lo), uint16(hi))
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
	//   hub.Subscribe("recorder", func(pts int64, au [][]byte, isIDR bool) { ... })
	//   ...
	//   _ = srv.ByeChannel(deviceID, channelID)
	select {}
}
