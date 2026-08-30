# gb28181-go

**English** | [中文](README.zh-CN.md)

[![CI](https://github.com/mickeyzzc/gb28181-go/actions/workflows/ci.yml/badge.svg)](https://github.com/mickeyzzc/gb28181-go/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
![Language: Go](https://img.shields.io/badge/language-Go-00ADD8.svg)
![Tests](https://img.shields.io/badge/tests-300%2B%20passing-brightgreen.svg)

GB/T 28181-2016/2022 libraries for Go — device (UAC) and platform (UAS) roles for the Chinese national video-surveillance standard.

Hand-written SIP (no SIP framework on the device side; the platform side builds on [ghettovoice/gosip](https://github.com/ghettovoice/gosip)), MANSCDP XML codec, RTP/PS media, and session orchestration. Extracted verbatim from Mi-Bee Studio production code, hardened against real GB28181 platforms (digest qop=auth, unique Via branches, local-IP detection, MANSCDP attribute form, SIP-over-TCP, TCP media).

## Packages

| Package | Role | Origin |
|---|---|---|
| `device/` | **UAC** — register a camera with a SIP platform: REGISTER + digest auth, catalog/deviceinfo/keepalive, INVITE-driven RTP/PS live streaming, RecordInfo, paced playback/download with SIP INFO control. UDP, TCP, and SIPS (TLS). | `mibee-eye-raspi-go` `internal/gb28181` |
| `manscdp/` | shared MANSCDP XML codec (element+attribute forms, GB2312/GBK/GB18030/UTF-8) | MiBeeNvr `internal/gb28181/manscdp` |
| `platform/` | **UAS** (migration batches 1–3/4 landed) — device/channel registry with keepalive liveness, MPEG-PS demux (PSM-less audio fallback heuristic), port pool, PTZ/A505 command building, RTP receive/reassembly (UDP + TCP-passive, jitter buffer, SSRC latch), INVITE/BYE SessionManager with FrameHub fan-out. | MiBeeNvr `internal/gb28181` |
| `platform/sip/` | SIP UAS server on gosip — REGISTER + digest auth (qop=auth), keepalive/offline detection, catalog refresh + SUBSCRIBE (Catalog/Alarm/MobilePosition), INVITE/BYE with firmware-quirk patches (speculative ACK, dialog reset, REGISTER source-port rotation, long-GOP IDR watchdog), playback/talk domains, alarm-triggered stream linkage. Persistence via the `DeviceStore` interface; events via the built-in lossy `EventBus`. | MiBeeNvr `internal/gb28181/sip` |
| `psmux/` | PS/RTP muxer (H.264/H.265, G.711 audio, >60KB AU splitting, RTP fragmentation) shared by device push and platform/cascade forwarding | MiBeeNvr `internal/gb28181/psmux` |
| `nalutil/` | NALU utilities (IDR detection, parameter-set extraction/comparison) — shared by platform receive and the future device side | MiBeeNvr `internal/model/nalutil` |
| `conformance/` | device↔platform loopback conformance suite — a real `device.Server` against a real platform SIP server on localhost: REGISTER+digest → catalog → keepalive liveness → INVITE live → byte-exact RTP/PS round-trip → BYE; plus the SIPS (TLS signaling) variant. Both roles must agree on every protocol reading, on every CI run. | new (issue #13) |
| `platform/cascade/` | Cascade client — this platform registers as a LOWER platform to an upper platform: aggregated catalog upload with stable first-seen channel IDs, INVITE forwarding (FrameHub subscribe → psmux → RTP), playback from recorded segments, BYE/SUBSCRIBE/MESSAGE/INFO/OPTIONS handling, protocol-level loopback tests. Local cameras via `CameraSource`, persistence via `Store`, segment reading via `SegmentParser` — all host-injected. | MiBeeNvr `internal/gb28181/cascade` |

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

## Examples

Runnable examples under [`examples/`](examples/) — each is a `main` package you can `go run`:

| Example | Role | Shows |
|---|---|---|
| [`device-register`](examples/device-register/main.go) | Device (UAC) | SIP registration, keepalive, catalog answers, synthetic frames through `device.FrameHub`, INVITE-driven streaming |
| [`platform-uas`](examples/platform-uas/main.go) | Platform (UAS) | SIP server with digest auth, in-memory `DeviceStore` seam, alarm `EventBus` subscription, INVITE/ByeChannel flow |
| [`psmux-rtp`](examples/psmux-rtp/main.go) | Media path | H.264 Annex-B → MPEG-PS → RTP packetization to a UDP receiver |

## Development

This project follows strict **TDD** — see [CONTRIBUTING.md](CONTRIBUTING.md). CI enforces `gofmt`, `go vet`, and `go test -race`; `main` is protected (PR-only merges, CI required).

## Status

`device/` + `manscdp/` shipped; `platform/` extraction complete (4/4 batches: PS demux/mux, registry, portmanager, PTZ, RTP receiver, SessionManager, SIP UAS server, cascade). API surfaces are settling but not frozen. Production-tested daily at [Mi-Bee Studio](https://github.com/Mi-Bee-Studio) against the MiBee NVR GB28181 platform.

# gb28181-go

**English** | [中文](README.zh-CN.md)

[![CI](https://github.com/mickeyzzc/gb28181-go/actions/workflows/ci.yml/badge.svg)](https://github.com/mickeyzzc/gb28181-go/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
![Language: Go](https://img.shields.io/badge/language-Go-00ADD8.svg)
![Tests](https://img.shields.io/badge/tests-300%2B%20passing-brightgreen.svg)

GB/T 28181-2016/2022 libraries for Go — device (UAC) and platform (UAS) roles for the Chinese national video-surveillance standard.

Hand-written SIP (no SIP framework on the device side; the platform side builds on [ghettovoice/gosip](https://github.com/ghettovoice/gosip)), MANSCDP XML codec, RTP/PS media, and session orchestration. Extracted verbatim from Mi-Bee Studio production code, hardened against real GB28181 platforms (digest qop=auth, unique Via branches, local-IP detection, MANSCDP attribute form, SIP-over-TCP, TCP media).

## Packages

| Package | Role | Origin |
|---|---|---|
| `device/` | **UAC** — register a camera with a SIP platform: REGISTER + digest auth, catalog/deviceinfo/keepalive, INVITE-driven RTP/PS live streaming, RecordInfo, paced playback/download with SIP INFO control. UDP, TCP, and SIPS (TLS). | `mibee-eye-raspi-go` `internal/gb28181` |
| `manscdp/` | shared MANSCDP XML codec (element+attribute forms, GB2312/GBK/GB18030/UTF-8) | MiBeeNvr `internal/gb28181/manscdp` |
| `platform/` | **UAS** (migration batches 1–3/4 landed) — device/channel registry with keepalive liveness, MPEG-PS demux (PSM-less audio fallback heuristic), port pool, PTZ/A505 command building, RTP receive/reassembly (UDP + TCP-passive, jitter buffer, SSRC latch), INVITE/BYE SessionManager with FrameHub fan-out. | MiBeeNvr `internal/gb28181` |
| `platform/sip/` | SIP UAS server on gosip — REGISTER + digest auth (qop=auth), keepalive/offline detection, catalog refresh + SUBSCRIBE (Catalog/Alarm/MobilePosition), INVITE/BYE with firmware-quirk patches (speculative ACK, dialog reset, REGISTER source-port rotation, long-GOP IDR watchdog), playback/talk domains, alarm-triggered stream linkage. Persistence via the `DeviceStore` interface; events via the built-in lossy `EventBus`. | MiBeeNvr `internal/gb28181/sip` |
| `psmux/` | PS/RTP muxer (H.264/H.265, G.711 audio, >60KB AU splitting, RTP fragmentation) shared by device push and platform/cascade forwarding | MiBeeNvr `internal/gb28181/psmux` |
| `nalutil/` | NALU utilities (IDR detection, parameter-set extraction/comparison) — shared by platform receive and the future device side | MiBeeNvr `internal/model/nalutil` |
| `conformance/` | device↔platform loopback conformance suite — a real `device.Server` against a real platform SIP server on localhost: REGISTER+digest → catalog → keepalive liveness → INVITE live → byte-exact RTP/PS round-trip → BYE; plus the SIPS (TLS signaling) variant. Both roles must agree on every protocol reading, on every CI run. | new (issue #13) |
| `platform/cascade/` | Cascade client — this platform registers as a LOWER platform to an upper platform: aggregated catalog upload with stable first-seen channel IDs, INVITE forwarding (FrameHub subscribe → psmux → RTP), playback from recorded segments, BYE/SUBSCRIBE/MESSAGE/INFO/OPTIONS handling, protocol-level loopback tests. Local cameras via `CameraSource`, persistence via `Store`, segment reading via `SegmentParser` — all host-injected. | MiBeeNvr `internal/gb28181/cascade` |

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

## Examples

Runnable examples under [`examples/`](examples/) — each is a `main` package you can `go run`:

| Example | Role | Shows |
|---|---|---|
| [`device-register`](examples/device-register/main.go) | Device (UAC) | SIP registration, keepalive, catalog answers, synthetic frames through `device.FrameHub`, INVITE-driven streaming |
| [`platform-uas`](examples/platform-uas/main.go) | Platform (UAS) | SIP server with digest auth, in-memory `DeviceStore` seam, alarm `EventBus` subscription, INVITE/ByeChannel flow |
| [`psmux-rtp`](examples/psmux-rtp/main.go) | Media path | H.264 Annex-B → MPEG-PS → RTP packetization to a UDP receiver |

## Development

This project follows strict **TDD** — see [CONTRIBUTING.md](CONTRIBUTING.md). CI enforces `gofmt`, `go vet`, and `go test -race`; `main` is protected (PR-only merges, CI required).

## Status

`device/` + `manscdp/` shipped; `platform/` extraction complete (4/4 batches: PS demux/mux, registry, portmanager, PTZ, RTP receiver, SessionManager, SIP UAS server, cascade). API surfaces are settling but not frozen. Production-tested daily at [Mi-Bee Studio](https://github.com/Mi-Bee-Studio) against the MiBee NVR GB28181 platform.

### API surface note: two PS muxers, two MANSCDP type sets

`device.MuxH264ToPS` (battle-tested against real platforms) and `psmux.New()`
(H.265 + G.711 audio capable) intentionally coexist, as do the `device/` and
`manscdp/` MANSCDP type sets: the device-side wire bytes are golden-tested
byte-exact against the Rust twin, so consolidation is deferred until a
wire-compat strategy exists. Pick `psmux` for new integrations needing
H.265/audio; use the `device/` built-ins when matching the twin's bytes
matters.

## Library hygiene (v0.3.0 hardening)

v0.3.0 removed product branding from the wire protocol so third parties can import this library as-is. The source-scan guard at the repo root (`hygiene_test.go`) pins each guarantee:

- **Configurable, neutral SIP User-Agent** — platform defaults to `gb28181-go`, cascade to `gb28181-go/cascade` (was hardcoded `MiBeeNvr-GB28181/1.0`). Override via `platform/sip.Config.UserAgent` / `platform/cascade.Config.UserAgent`.
- **Neutral catalog/DeviceInfo identity** — cascade catalog items default `Manufacturer`/`Model` to `Unknown` (was `MiBee`/`MiBeeNvr`), overridable via `CatalogDefaultManufacturer`/`CatalogDefaultModel`.
- **Device dialog To-tag from crypto/rand** — 8 hex chars, no product prefix, not time-derived (RFC 3261 §19.3).
- **Platform logger follows slog.SetDefault** — derived from the current default logger on every call, so hosts can retarget at any time (the init-time binding mismatch is fixed).
- **INVITE answer timeout is configurable** — `platform/sip.Config.InviteResponseTimeout` (default 32s).
- **`device.FormatDeviceID`/`ParseDeviceID`** — the previously empty stub now implements 20-digit GB ID formatting/parsing (errors returned, never panicked).
- **Package comments fixed** — the 9 wrong `// Package gb28181` comments in `device/` are corrected.

## License

MIT — see [LICENSE](LICENSE).


v0.3.0 removed product branding from the wire protocol so third parties can import this library as-is. The source-scan guard at the repo root (`hygiene_test.go`) pins each guarantee:

- **Configurable, neutral SIP User-Agent** — platform defaults to `gb28181-go`, cascade to `gb28181-go/cascade` (was hardcoded `MiBeeNvr-GB28181/1.0`). Override via `platform/sip.Config.UserAgent` / `platform/cascade.Config.UserAgent`.
- **Neutral catalog/DeviceInfo identity** — cascade catalog items default `Manufacturer`/`Model` to `Unknown` (was `MiBee`/`MiBeeNvr`), overridable via `CatalogDefaultManufacturer`/`CatalogDefaultModel`.
- **Device dialog To-tag from crypto/rand** — 8 hex chars, no product prefix, not time-derived (RFC 3261 §19.3).
- **Platform logger follows slog.SetDefault** — derived from the current default logger on every call, so hosts can retarget at any time (the init-time binding mismatch is fixed).
- **INVITE answer timeout is configurable** — `platform/sip.Config.InviteResponseTimeout` (default 32s).
- **`device.FormatDeviceID`/`ParseDeviceID`** — the previously empty stub now implements 20-digit GB ID formatting/parsing (errors returned, never panicked).
- **Package comments fixed** — the 9 wrong `// Package gb28181` comments in `device/` are corrected.

## License

MIT — see [LICENSE](LICENSE).
