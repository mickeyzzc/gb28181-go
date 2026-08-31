# Device (UAC): registering a camera with a platform

The `device` package turns a Go program into a GB/T 28181 camera:
REGISTER with digest auth, keepalive, catalog answering, and
INVITE-driven RTP/PS streaming — over UDP, TCP, or SIPS (TLS).

## Config reference

```go
cfg := device.Config{
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
    Transport:             "udp", // "udp" | "tcp" | "tls"
}
```

YAML tags match (`platform_sip_address`, …) — the struct can be
unmarshalled straight from your app's `gb28181:` section.

For `Transport: "tls"` (SIPS, GB/T 28181-2022 A-level), provision the
TLS fields: `TLSCAFile` (the platform's self-signed cert or CA),
optional `TLSCertFile`/`TLSKeyFile` for mutual auth, and
`TLSInsecureSkipVerify` for lab use only. The GB convention is a
self-signed CA whose serial is the device/platform ID.

## Identity

```go
srv := device.New(cfg, device.DeviceInfo{
    Name:         "Front gate",
    Manufacturer: "Acme",
    Model:        "Cam-X",
    Firmware:     "1.2.3",
    HardwareID:   "CamX-SoC",
    SerialNumber: "SN-42",
}, hub)
```

`device.UserAgent` (package var, default `GB28181-Go/1.0`) stamps the
SIP User-Agent — assign your own **before** `New`/`Start`; concurrent
mutation afterwards races the message builders.

## Live frames: FrameSource

```go
type FrameSource interface {
    Subscribe(ctx context.Context) *FrameSubscription
    Unsubscribe(id string)
}

type FrameSubscription struct {
    ID      string
    Channel <-chan AccessUnit
}
```

Push `AccessUnit` values (NALUs without Annex-B start codes,
`Timestamp`, `KeyFrame`) into your hub; the server subscribes on
INVITE and pushes RTP/PS to the platform's media port. Bounded,
drop-on-full channels are expected — never block your encoder on a
slow consumer. `device.NewFrameHub()` is a ready-made implementation;
`SIPPort()`/`SIPTCPPort()` report the bound ports.

## Recordings: RecordingIndex

```go
srv.SetRecordingIndex(myIndex) // implements Lookup(startMs, endMs) []SegmentMeta
```

With an index attached, the server answers RecordInfo queries and
serves paced playback and full-speed download, including SIP INFO
playback control (pause/resume/seek/speed). Segment files use the
reference format read by `device.OpenSegment`: bare Annex-B H.264 +
per-frame `.ts.jsonl` sidecar.

## Lifecycle

```go
err := srv.Start(ctx) // blocks? no — see below
...
srv.Stop()
```

`Start` runs registration/keepalive/streaming goroutines under `ctx`
cancellation; `Stop` tears them down. Registration retries with backoff
and re-REGISTERs on expiry.

## Device IDs

```go
id, err := device.FormatDeviceID("34020000", 0, device.DeviceTypeIPC, 42)
parts, err := device.ParseDeviceID(id) // center/industry/type/serial
```

20-digit codes: `[8 region][2 industry][3 type][7 serial]`. Type
constants include `DeviceTypeIPC` (111), `DeviceTypeNVR` (118),
`DeviceTypeAlarm` (122).
