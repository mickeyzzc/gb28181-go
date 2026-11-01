package main

// device-register: the device-side (UAC) emulator — the same sample that
// lived at examples/device-register, with the hardcoded configuration
// promoted to flags so a prebuilt binary is actually usable.

import (
	"context"
	"flag"
	"log"
	"net"
	"strconv"
	"time"

	"github.com/mickeyzzc/gb28181-go/device"
)

func runDeviceRegister(args []string) {
	fs := flag.NewFlagSet("device-register", flag.ExitOnError)
	platform := fs.String("platform", "192.168.1.10:5060", "platform SIP address host:port")
	deviceID := fs.String("device-id", "34020000001320000001", "20-digit device ID")
	channelID := fs.String("channel-id", "34020000001310000001", "20-digit channel (camera) ID")
	domain := fs.String("domain", "3402000000", "SIP domain (usually the first 10 digits)")
	password := fs.String("password", "", "digest-auth secret agreed with the platform (required)")
	localPort := fs.Int("local-sip-port", 5060, "local SIP port")
	name := fs.String("name", "Front Door", "device name reported in DeviceInfo")
	// Neutral defaults per the v0.3.0 wire-neutrality hardening: the
	// catalog and DeviceInfo answers must not carry product branding.
	manufacturer := fs.String("manufacturer", "Unknown", "manufacturer string reported in DeviceInfo/catalog")
	model := fs.String("model", "Unknown", "model string reported in DeviceInfo/catalog")
	transport := fs.String("transport", "udp", "signaling transport: udp | tcp | tls (SIPS, GB/T 28181-2022)")
	interval := fs.Int("register-interval", 3600, "registration refresh interval, seconds")
	heartbeat := fs.Int("heartbeat-interval", 60, "keepalive interval, seconds")
	_ = fs.Parse(args)

	if *password == "" {
		log.Fatal("device-register: -password is required (the digest secret your platform expects)")
	}
	host, portStr, err := net.SplitHostPort(*platform)
	if err != nil {
		log.Fatalf("device-register: -platform must be host:port, got %q: %v", *platform, err)
	}
	platformPort, err := strconv.Atoi(portStr)
	if err != nil {
		log.Fatalf("device-register: -platform port not numeric: %v", err)
	}

	cfg := device.Config{
		Enabled:               true,
		PlatformSIPAddress:    host,
		PlatformSIPPort:       platformPort,
		DeviceID:              *deviceID,
		ChannelID:             *channelID,
		SIPDomain:             *domain,
		Password:              *password,
		LocalSIPPort:          *localPort,
		RegisterIntervalSecs:  *interval,
		HeartbeatIntervalSecs: *heartbeat,
		HeartbeatTimeoutCount: 3,
		Transport:             *transport,
	}

	// The hub is a complete FrameSource: bounded channels, drop-on-full.
	// In production, wire your encoder's output here instead of the
	// synthetic loop below.
	hub := device.NewFrameHub()

	srv := device.New(cfg, device.DeviceInfo{
		Name:         *name,
		Manufacturer: *manufacturer,
		Model:        *model,
	}, hub)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Start(ctx) }() // Start blocks; registration,
	// keepalive, catalog answers and INVITE handling all run inside.

	// Synthetic capture loop: one IDR every five ticks, P frames between.
	// Real video comes from the camera encoder as NALU access units.
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	var exitErr error
	defer func() {
		if exitErr != nil {
			log.Fatalf("device server: %v", exitErr)
		}
	}()
	for i := 0; ; i++ {
		select {
		case <-ctx.Done():
			srv.Stop()
			return
		case <-ticker.C:
			au := device.AccessUnit{
				Timestamp: time.Now(),
				KeyFrame:  i%5 == 0,
			}
			if au.KeyFrame {
				au.NALUs = []device.NALU{
					{Type: 7, IsSPS: true, Data: []byte{0x67, 0x42}},
					{Type: 8, IsPPS: true, Data: []byte{0x68, 0xEB}},
					{Type: 5, IsIDR: true, Data: []byte{0x65, 0x01}},
				}
			} else {
				au.NALUs = []device.NALU{{Type: 1, Data: []byte{0x41, 0x02}}}
			}
			hub.Write(au)
		case err := <-errCh:
			// Exit the loop first so the deferred ticker.Stop() runs —
			// log.Fatalf would skip it.
			exitErr = err
			return
		}
	}
}
