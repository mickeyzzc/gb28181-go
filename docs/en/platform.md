# Platform (UAS): the SIP server side

`platform/sip` is a GB/T 28181 platform: devices register to **you**.
Built on [gosip], it handles the REGISTER/keepalive/catalog/INVITE
lifecycle; you inject persistence and consume events.

## Config

```go
cfg := sip.Config{
    Enabled:               true,
    SIPListen:             ":5060",
    ServerID:              "34020000002000000001",
    Realm:                 "3402000000",
    Password:              "secret",
    UserAgent:             "",                  // "" = "gb28181-go"
    InviteResponseTimeout: "32s",               // Go duration string
    PortRange:             "30000-30050",       // RTP media port pool
    AllowedDeviceIDs:      nil,                 // nil = allow any
}
```

`UserAgent` is the neutrality override — the default carries no
product brand; set it if your upstream fingerprint expects one.
`InviteResponseTimeout` bounds how long `InviteChannel` waits for a
device's INVITE answer.

## Persistence: DeviceStore

You own the database; the server calls:

```go
type DeviceStore interface {
    UpsertGB28181Device(ctx, GB28181Device) error
    UpsertGB28181Channel(ctx, GB28181Channel) error
    ListGB28181Devices(ctx) ([]GB28181Device, error)
    ListGB28181Channels(ctx, deviceID string) ([]GB28181Channel, error)
    MarkDeviceOffline(ctx, id) error
    BindChannelCamera(ctx, channelID, cameraID) error
    GetGB28181Device(ctx, id) (*GB28181Device, error)
    DeleteGB28181Device(ctx, id) error
    DeleteGB28181Channel(ctx, channelID) error
}
```

An in-memory implementation ships for tests and the
`examples/platform-uas` demo.

## Events: EventBus

```go
bus := sip.NewEventBus(64) // lossy: slow consumers drop, never block
// subscribe to device online/offline, alarm, registration events
```

The bus is deliberately lossy — events are notifications, not a queue.

## Sessions and liveness

- Registration is challenged with digest auth (`qop=auth`); keepalive
  MESSAGEs drive liveness — silence past the timeout marks the device
  offline via `MarkDeviceOffline`.
- INVITE/BYE sessions flow through the `SessionManager` with
  `platform.FrameHub` fan-out to multiple viewers.
- The receive path: RTP reassembly (UDP and TCP-passive), jitter
  buffer, SSRC latch, MPEG-PS demux back to NAL units.

## Firmware-quirk patches

The server carries patches learned from real devices: speculative ACK,
dialog reset, REGISTER source-port rotation, and a long-GOP IDR
watchdog. They activate on detected behavior, not config.

## Conformance

`conformance/` runs a real `device.Server` against a real platform
SIP server on localhost — REGISTER+digest → catalog → keepalive →
INVITE live → byte-exact RTP/PS round-trip → BYE — on every CI run,
including the SIPS (TLS) variant. If you change either role, this
suite arbitrates.

[gosip]: https://github.com/ghettovoice/gosip
