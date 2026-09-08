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
| `security35114/` | **opt-in, build-tagged (`-tags gb35114`)** — GB 35114-2017 **A-level** security for **both sides**: device-side SM2-certificate mutual authentication over the REGISTER flow (`device.Config.RegisterAuthenticator`) and platform-side challenge/verify/Note state machine (`security35114.Platform`, wired through `platform/sip`'s `Config.RegisterAuthenticator`). VKEK negotiation inside the `cryptkey` SM2 envelope, keyed-SM3 `Note`-header integrity for subsequent signaling. | new (v0.4.0, platform side v0.5.0) |

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

## GB35114 A-level security (v0.4.0 device / v0.5.0 platform, opt-in)

[GB 35114-2017](https://openstd.samr.gov.cn/bzgk/std/newGbInfo?hcno=B7F5589329EF98B32F0EB8ACEC341C81) layers SM2-certificate security on top of GB/T 28181. Only **A-level** is implemented — levels B/C additionally require SVAC media (GB/T 25724, a hardware codec), which is out of scope by design. The package lives behind the `gb35114` build tag so default builds stay dependency-light:

```go
// go build -tags gb35114
import sec "github.com/mickeyzzc/gb28181-go/security35114"

auth, err := sec.New(sec.Options{
    Device:       devIdentity,     // sec.LoadIdentityFromFiles(cert, key)
    PlatformCert: platCert,        // sec.LoadCertificate(cert) — verifies sign2
    DeviceID:     "34020000001320000001",
    ServerID:     "34020000002000000001",
})
cfg.RegisterAuthenticator = auth   // replaces Digest auth in the REGISTER lifecycle
```

The handshake follows the published standard text cross-checked against real captures: `Capability` announcement → 401 with `random1` → signed re-REGISTER (`sign1` = SM2 over random2‖random1‖serverID) → 200 OK `SecurityInfo` carrying the SM2-sealed VKEK (`cryptkey`, DER C1‖C3‖C2 envelope) and, for `Bidirection`, the platform's `sign2`. After registration every outgoing request (keepalive, …) carries `Date` + `Note: Digest nonce="…",algorithm=SM3` keyed by the VKEK. Golden wire strings pin each header; an end-to-end test drives a real `device.Server` against a fake platform that verifies `sign1`, unseals the VKEK, and validates the first keepalive's `Note`.

Two points are ambiguous across implementations and therefore configurable (`RandomEncoding`, `Sign2Order`): the random representation inside the signed payload, and the R1/R2 operand order of `sign2`. Defaults match the standard text (wire-strings concatenation observed in captures; R1-first). SM3/SM2 come from [emmansun/gmsm](https://github.com/emmansun/gmsm) (pure Go, GM/T 0015-2012 SM2 X.509 certificates).

Wire-format caveats: certificate provisioning is out of band (or `Options.IncludeDeviceCert` for platforms that accept the `cnonce` announcement), and incoming platform requests are not `Note`-verified on the device side yet.

### Platform side (UAS, v0.5.0)

`security35114.Platform` is the mirror-image state machine for GB/T 28181 platforms: it issues `Bidirection`/`Unidirection` challenges, verifies device `sign1` against the (pre-provisioned or `cnonce`-announced) certificate, seals the VKEK, signs `sign2`, and verifies every subsequent `Note`. `platform/sip.Server` routes non-Digest REGISTER schemes to it and attaches the `SecurityInfo` header to the 200 OK — Digest devices keep flowing through `Password` unchanged:

```go
// go build -tags gb35114
plat35114, err := sec.NewPlatform(sec.PlatformConfig{
    ServerID:    "34020000002000000001",
    Identity:    platIdentity, // platform SM2 signing cert+key — sign2
    DeviceCerts: map[string]*smx509.Certificate{deviceID: devCert}, // or trust the cnonce announcement
})
sipCfg.RegisterAuthenticator = plat35114 // platform/sip.Config; digest path untouched
```

`Platform` is safe for concurrent use, keys sessions by device ID, keeps the previous VKEK verifying while a device re-registers, answers SIP-over-UDP retransmissions of the completed REGISTER idempotently, and rejects stale `random1` (replay), scheme mismatches, and unknown/mismatched device certificates with sentinel errors (`ErrChallengeMismatch`, `ErrDeviceCert`, …) that map onto 4xx responses. An in-process loopback test drives a real `device.Server` with the device-side authenticator against a real `platform/sip.Server` with `Platform` wired in — handshake, VKEK agreement, and `Note` verification all under real SM2/SM3.

### GB28181-2022 snapshot & manual recording (issue #49)

`DeviceControl` carries the 2022 image-snapshot command (`SnapShot`: `SnapNum`/`Interval`/`UploadURL`/`SessionID` per A.2.1.24), `manscdp.Decode` parses the `UploadSnapShotFinished` completion notify (A.2.5.7, `SnapShotList` of `SnapShotFileID`), and `platform.PTZController` grows `SendSnapShotCmd` / `StartManualRecord` / `StopManualRecord`.

## Documentation

Topic guides live under [`docs/en/`](docs/en/) — each has a Chinese counterpart under `docs/zh/`:

| Guide | Covers |
|---|---|
| [Device (UAC)](docs/en/device.md) | full `device.Config` reference, `FrameSource`/`FrameHub`, recordings & playback, UDP/TCP/TLS, device IDs |
| [MANSCDP codec](docs/en/manscdp.md) | message types, element/attribute dual form, GB2312/GBK/GB18030/UTF-8 charsets |
| [PS muxer & RTP](docs/en/psmux.md) | `psmux.Muxer`, RTP packetizers (UDP/TCP), which muxer to pick, `nalutil` |
| [Platform (UAS)](docs/en/platform.md) | `platform/sip` server: config, `DeviceStore`, `EventBus`, session manager, liveness |
| [Cascade client](docs/en/cascade.md) | registering to an upper platform: `CameraSource`/`Store`/`SegmentParser` seams |

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
