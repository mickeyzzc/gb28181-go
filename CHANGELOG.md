# Changelog

All notable changes to this project are documented here. The format is
based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/); the
project follows [semantic versioning](https://semver.org/). Wire-format
golden strings are contracts — any golden change is a breaking change.

Releases are capability packages: merges accumulate on `main` silently
and ship with the next tag (merge ≠ release). Only urgent security fixes
are released out of band.

## [Unreleased]

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
