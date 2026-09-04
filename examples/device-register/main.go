// Device-side (UAC) minimal example: a camera registers with a GB/T 28181
// platform over SIP, answers Catalog/DeviceInfo queries, and streams
// RTP/PS media when the platform INVITEs. Frames are fed through the
// ready-made device.FrameHub — bridge your capture pipeline onto
// hub.Write the same way.
package main

import (
	"context"
	"log"
	"time"

	"github.com/mickeyzzc/gb28181-go/device"
)

func main() {
	cfg := device.Config{
		Enabled:               true,
		PlatformSIPAddress:    "192.168.1.10",
		PlatformSIPPort:       5060,
		DeviceID:              "34020000001320000001",
		ChannelID:             "34020000001310000001",
		SIPDomain:             "3402000000",
		Password:              "your-platform-password", // digest-auth secret agreed with the platform — set your own
		LocalSIPPort:          5060,
		RegisterIntervalSecs:  3600,
		HeartbeatIntervalSecs: 60,
		HeartbeatTimeoutCount: 3,
		Transport:             "udp", // udp | tcp | tls (SIPS, GB/T 28181-2022)
	}

	// The hub is a complete FrameSource: bounded channels, drop-on-full.
	// In production, wire your encoder's output here instead of the
	// synthetic loop below.
	hub := device.NewFrameHub()

	srv := device.New(cfg, device.DeviceInfo{
		Name:         "Front Door",
		Manufacturer: "MiBee Studio",
		Model:        "MiBee Eye",
	}, hub)

	// Optional: recorded-segment index for RecordInfo/playback INVITEs.
	// srv.SetRecordingIndex(myIndex)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Start(ctx) }() // Start blocks; registration,
	// keepalive, catalog answers and INVITE handling all run inside.

	// Synthetic capture loop: one IDR + one P frame per second. Real video
	// comes from the camera encoder as NALU access units.
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
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
			// Placeholder payloads — replace with encoder output.
			if au.KeyFrame {
				au.NALUs = []device.NALU{
					{Type: 7, IsSPS: true, Data: []byte{0x67, 0x42}},
					{Type: 8, IsPPS: true, Data: []byte{0x68, 0xEb}},
					{Type: 5, IsIDR: true, Data: []byte{0x65, 0x01}},
				}
			} else {
				au.NALUs = []device.NALU{{Type: 1, Data: []byte{0x41, 0x02}}}
			}
			hub.Write(au)
		case err := <-errCh:
			log.Fatalf("device server: %v", err)
		}
	}
}
