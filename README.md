# gb28181-go

**English** | [中文](README.zh-CN.md)

[![CI](https://github.com/mickeyzzc/gb28181-go/actions/workflows/ci.yml/badge.svg)](https://github.com/mickeyzzc/gb28181-go/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
![Language: Go](https://img.shields.io/badge/language-Go-00ADD8.svg)
![Tests](https://img.shields.io/badge/tests-89%2B%20passing-brightgreen.svg)

GB/T 28181-2016/2022 libraries for Go — device (UAC) and platform (UAS) roles for the Chinese national video-surveillance standard.

Hand-written SIP (no SIP framework on the device side; the platform side builds on [ghettovoice/gosip](https://github.com/ghettovoice/gosip)), MANSCDP XML codec, RTP/PS media, and session orchestration. Extracted verbatim from Mi-Bee Studio production code, hardened against real GB28181 platforms (digest qop=auth, unique Via branches, local-IP detection, MANSCDP attribute form, SIP-over-TCP, TCP media).

## Packages

| Package | Role | Origin |
|---|---|---|
| `device/` | **UAC** — register a camera with a SIP platform: REGISTER + digest auth, catalog/deviceinfo/keepalive, INVITE-driven RTP/PS live streaming, RecordInfo, paced playback/download with SIP INFO control. UDP and TCP. | `mibee-eye-raspi-go` `internal/gb28181` |
| `manscdp/` | shared MANSCDP XML codec (element+attribute forms, GB2312/GBK/GB18030/UTF-8) | MiBeeNvr `internal/gb28181/manscdp` |
| `platform/` | **UAS** (migration batch 1/4 landed) — device/channel registry with keepalive liveness, MPEG-PS demux (PSM-less audio fallback heuristic), port pool, PTZ/A505 command building. RTP receive/session orchestration/SIP UAS server land in later batches. | MiBeeNvr `internal/gb28181` |
| `psmux/` | PS/RTP muxer (H.264/H.265, G.711 audio, >60KB AU splitting, RTP fragmentation) shared by device push and platform/cascade forwarding | MiBeeNvr `internal/gb28181/psmux` |

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

## Development

This project follows strict **TDD** — see [CONTRIBUTING.md](CONTRIBUTING.md). CI enforces `gofmt`, `go vet`, and `go test -race`; `main` is protected (PR-only merges, CI required).

## Status

`device/` + `manscdp/` shipped; `platform/` extraction batch 1 of 4 landed (PS demux/mux, registry, portmanager, PTZ). API surfaces are settling but not frozen. Production-tested daily at [Mi-Bee Studio](https://github.com/Mi-Bee-Studio) against the MiBee NVR GB28181 platform.

## License

MIT — see [LICENSE](LICENSE).
