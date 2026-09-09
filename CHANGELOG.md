# Changelog

All notable changes to this project are documented here. The format is
based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/); the
project follows [semantic versioning](https://semver.org/). Wire-format
golden strings are contracts — any golden change is a breaking change.

Releases are capability packages: merges accumulate on `main` silently
and ship with the next tag (merge ≠ release). Only urgent security fixes
are released out of band.

## [Unreleased]

- `feat(sip)` snapshot-finished event (#54): the 2022 UploadSnapShotFinished
  notify (MESSAGE, A.2.5.7) now publishes `gb28181.snapshot.finished` with
  DeviceID, SessionID, and the SnapShotList — hosts close their pending
  snapshot sessions on it; an empty list means the capture or upload
  failed wholly or partly.

- `docs` no spec-example password in quickstart (#34): the READMEs and the
  device-register / platform-uas examples pass a placeholder instead of
  the well-known `12345678` — docs and examples must never ship a value
  someone might run in production as-is.

## [v0.7.0] — 2026-09-09

- `test` soak harness (#45): `GB28181_SOAK=1 go test -run TestSoak
  ./conformance/` drives N (default 100) INVITE→frames→BYE cycles over
  one registered loopback pair with keepalives flowing, asserting no
  descriptor growth across session/media-port teardown.
- `bench` new benchmarks (#45): PS muxing keyframe/P-frame throughput,
  SIP wire parse (REGISTER/catalog MESSAGE), PortManager Get/Recycle
  serial + parallel.

- `refactor` net.Dial/Listen → DialContext/ListenContext across the
  device and platform dials/listens (#58): the lifecycle context now
  interrupts route probes, media dials, the SIPS handshake, and SIP TCP
  binds at graceful shutdown. Wire behavior is unchanged (the 5s media
  dial timeouts are preserved); the blanket noctx/contextcheck exemption
  for `device/`+`platform/` is gone, replaced by three targeted
  structural exemptions (gosip request handlers carry no ctx).

- `feat(gb35114)` device-side downstream `Note` verification (#52): after
  the A-level handshake, platform→device requests carrying a Note are
  verified against the negotiated VKEK with a ±5-minute Date freshness
  window (the replay guard — the digest alone is self-consistent).
  Failure behavior is `device.Config.IncomingNotePolicy`: 403 under the
  default Reject, log-only Warn for rollout observation, Off. Note-less
  requests keep passing (mixed-mode Digest platforms), mirroring the
  platform-side VerifyNote. New `device.IncomingNoteVerifier` seam;
  `security35114.Authenticator` implements it. Verified end-to-end
  against a fake platform signing with the real crypto.
- `ci(release)` the tag workflow now publishes a GitHub Release (#46):
  per-platform tool bundles (device-register / platform-uas /
  psmux-rtp, tar.gz + zip for Windows) across a 7-platform matrix
  (darwin/amd64 and windows/arm64 added), SHA256SUMS, and
  `gh release create --verify-tag --generate-notes`. Whole-library
  cross-compilation stays in the loop; merging to `main` still
  releases nothing — only a `v*.*.*` tag does.
- `feat(cascade)` REGISTER retry switched from a flat 15s to exponential
  backoff — base doubles per consecutive failure (default 1s, capped at
  5m) and resets on a successful registration; configurable via
  `register_retry_base`/`register_retry_max` (#44)
- `feat` new `backoff` package: the generic deterministic exponential
  backoff utility behind the cascade retry (#44)
- `feat(manscdp)` GB28181-2022 snapshot control + completion notify,
  platform convenience helpers (#50)
- `test(cascade)` TCP media-forward budget widened to 15s under CI load (#53)

## [v0.6.0] — 2026-09-08

**Added** security hardening: SIP framing limits on the device TCP reader
(`MaxSIPMessageSize` + full-body reads), REGISTER brute-force lockout on
the platform, and parser fuzz targets. (#48)

## [v0.5.0] — 2026-09-08

**Added** `security35114` platform (UAS) side of GB 35114 A-level —
challenge/verify/Note-verification built on the same golden vectors as
the Rust twin — plus the `platform/sip` seam. (#36)

## [v0.4.0] — 2026-09-08

- **Added** GB 35114 A-level device security, opt-in via `-tags gb35114`. (#35)
- **Added** tag-triggered release gate: full test suite + 5-platform
  cross-compile. (#32)
- **Fixed** test port ranges moved below the ephemeral range. (#33)

## [v0.3.0] — 2026-08-31

- **Changed (breaking)** library neutrality: configurable/neutral
  User-Agent and catalog identity, random dialog tags, neutral device
  IDs. (#30)
- **Fixed** FrameHub IDR flag exposure, neutral UserAgent defaults (#29);
  cascade/PS-demux PSM on a session's first burst + PSM-less codec latch (#22).
- **Added** runnable examples for the three usage roles (#24); coverage
  pushed 73.1% → 81%+ across packages (#23, #25); SIP test teardown race
  fixed (#26).

## [v0.2.2] — 2026-08-30

**Fixed** cascade + PS demux: PSM on a session's first burst; PSM-less
codec latch.

## [v0.2.1] — 2026-08-29

- **Added** platform package (batches 1–4): PS demux/mux, port manager,
  PTZ, device registry, RTP receiver + SessionManager, SIP UAS core with
  hook seams, cascade client (#15–#18); device↔platform conformance
  loopback suite (#19, caught a double-AU broadcast + Stop race).
- **Added** GB28181-2022 items: SVAC stream types + voice broadcast (#20);
  TLS signaling (SIPS) for both roles + conformance loopback (#21).

## [v0.2.0] — 2026-08-29

**Fixed** PS mux: PES_packet_length balanced with written bytes; >64 KB
access units split. LICENSE moved to repo root.

## [v0.1.1] — 2026-08-29

**Fixed** device honors TCP media offers in INVITE SDP + 4-byte `$`
framing. (#3)

## [v0.1.0] — 2026-08-28

Initial release: GB/T 28181-2016/2022 device (UAC) package — SIP
signaling, SDP, RTP/PS, MANSCDP codec (element+attribute forms, GB2312/
GBK/GB18030/UTF-8), recording/playback index seams.

[Unreleased]: https://github.com/mickeyzzc/gb28181-go/compare/v0.6.0...HEAD
[v0.6.0]: https://github.com/mickeyzzc/gb28181-go/compare/v0.5.0...v0.6.0
[v0.5.0]: https://github.com/mickeyzzc/gb28181-go/compare/v0.4.0...v0.5.0
[v0.4.0]: https://github.com/mickeyzzc/gb28181-go/compare/v0.3.0...v0.4.0
[v0.3.0]: https://github.com/mickeyzzc/gb28181-go/compare/v0.2.2...v0.3.0
[v0.2.2]: https://github.com/mickeyzzc/gb28181-go/compare/v0.2.1...v0.2.2
[v0.2.1]: https://github.com/mickeyzzc/gb28181-go/compare/v0.2.0...v0.2.1
[v0.2.0]: https://github.com/mickeyzzc/gb28181-go/compare/v0.1.1...v0.2.0
[v0.1.1]: https://github.com/mickeyzzc/gb28181-go/compare/v0.1.0...v0.1.1
[v0.1.0]: https://github.com/mickeyzzc/gb28181-go/releases/tag/v0.1.0
