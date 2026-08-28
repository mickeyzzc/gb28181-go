# gb28181-go

GB/T 28181-2016/2022 libraries for Go — device (UAC) and platform (UAS) roles for the Chinese national video-surveillance standard.

Hand-written SIP (no SIP framework on the device side; the platform side builds on [ghettovoice/gosip](https://github.com/ghettovoice/gosip)), MANSCDP XML codec, RTP/PS media, and session orchestration. Extracted verbatim from Mi-Bee Studio production code, hardened against real GB28181 platforms (digest qop=auth, unique Via branches, local-IP detection, MANSCDP attribute form, SIP-over-TCP, TCP media).

## Packages

| Package | Role | Origin |
|---|---|---|
| `device/` | **UAC** — register a camera with a SIP platform: REGISTER + digest auth, catalog/deviceinfo/keepalive, INVITE-driven RTP/PS live streaming, RecordInfo, paced playback/download with SIP INFO control. UDP and TCP. | `mibee-eye-raspi-go` `internal/gb28181` |
| `manscdp/` | *(planned)* shared MANSCDP XML codec | MiBeeNvr `internal/gb28181/manscdp` |
| `platform/` | *(planned)* **UAS** — SIP platform: device/channel management, INVITE/BYE sessions, RTP receive + PS demux | MiBeeNvr `internal/gb28181` |

## Usage (device)

```go
import "github.com/mickeyzzc/gb28181-go/device"

cfg := device.Config{
    PlatformSIPAddress: "192.168.1.10",
    PlatformSIPPort:    5060,
    DeviceID:           "34020000001320000001",
    ChannelID:          "34020000001310000001",
    SIPDomain:          "3402000000",
    Password:           "12345678",
}

srv := device.New(cfg, device.DeviceInfo{Name: "My Cam"}, frameSource)
// frameSource implements device.FrameSource over your capture hub;
// optionally srv.SetRecordingIndex(...) for RecordInfo/playback.
err := srv.Start(ctx)
```

Host seams (interfaces, host-injected):
- `FrameSource` — live H.264 access units
- `RecordingSource` — recorded-segment index for RecordInfo/playback
- `Config` / `DeviceInfo` — settings and identity (YAML shapes unchanged from the source projects)

Segment files use the reference format read by `device.OpenSegment`: bare Annex-B H.264 + per-frame `.ts.jsonl` sidecar. A ready-made `device.FrameHub` implements `FrameSource` with bounded-channel, drop-on-full semantics for tests.

## Status

v0.1.0 — `device/` shipped; `platform/` + `manscdp/` extraction in progress. API surfaces are settling but not frozen. Production-tested daily at [Mi-Bee Studio](https://github.com/Mi-Bee-Studio) against the MiBee NVR GB28181 platform.

## License

MIT — see [LICENSE](LICENSE).
