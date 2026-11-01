# Quick start: register a camera with a platform

The `device` package turns a Go program into a GB/T 28181 camera in ten
minutes: it registers with the platform, answers keepalive and catalog
queries, and streams RTP/PS when an INVITE arrives. This page is the
minimal compilable example; per-module depth follows in later chapters.

## Install

```bash
go get github.com/mickeyzzc/gb28181-go@v0.11.0
```

## Minimal example

```go
package main

import (
	"context"
	"log"

	"github.com/mickeyzzc/gb28181-go/device"
)

func main() {
	hub := device.NewFrameHub()
	srv := device.New(device.Config{
		Enabled:               true,
		PlatformSIPAddress:    "192.0.2.10",
		PlatformSIPPort:       5060,
		DeviceID:              "34020000001320000001",
		ChannelID:             "34020000001310000001",
		SIPDomain:             "3402000000",
		Password:              "secret",
		LocalSIPPort:          5060,
		RegisterIntervalSecs:   60,
		HeartbeatIntervalSecs: 60,
		HeartbeatTimeoutCount: 3,
		Transport:             "udp",
	}, device.DeviceInfo{
		Name:         "Front gate",
		Manufacturer: "Acme",
		Model:        "Cam-X",
		Firmware:     "1.2.3",
		HardwareID:   "SoC",
		SerialNumber: "SN-42",
	}, hub)

	if err := srv.Start(context.Background()); err != nil {
		log.Fatal(err)
	}
	defer srv.Stop()
	select {} // block; push AccessUnits into hub from your capture loop
}
```

Notes:

- `Start` runs the REGISTER digest handshake and the keepalive loop;
  `Stop` tears down cleanly.
- On a platform INVITE the server subscribes `hub` for live frames and
  pushes RTP/PS — your capture pipeline only feeds `AccessUnit` values
  (NALs without start codes) into the hub.
- Example values use documentation-safe ranges: `192.0.2.x` addresses,
  GB IDs from the `3402000000…` example block.

## Next steps

- [Device manual](gb28181-go-device.md): full config table, TLS (SIPS),
  recording index, device IDs.
- [Platform](gb28181-go-platform.md) and [cascade](gb28181-go-cascade.md):
  the reverse roles.
- [MANSCDP](gb28181-go-manscdp.md) and [PS mux](gb28181-go-psmux.md):
  message and media-plane details.
