# Changelog

All notable changes to this project are documented here. The format is
based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/); the
project follows [semantic versioning](https://semver.org/). Wire-format
golden strings are contracts — any golden change is a breaking change.

Releases are capability packages: merges accumulate on `main` silently
and ship with the next tag (merge ≠ release). Only urgent security fixes
are released out of band.

## [Unreleased]

- **BREAKING-packaging**: the release tools are now **one binary** —
  `cmd/gb28181` with the subcommands `device-register` / `platform-uas`
  / `psmux-rtp`. The three prebuilt `examples/` binaries hard-coded
  their configuration in source, so the shipped binaries could not be
  pointed at anything without a recompile; every subcommand is
  flag-driven now (`-platform`, `-device-id`, `-password`,
  `-transport`, …). Release bundles change from
  `gb28181-go-tools_<os>_<arch>` (three binaries inside) to a single
  `gb28181-cli_<os>_<arch>` archive per platform. `examples/` is gone —
  the subcommand files are the runnable integration samples.
- `feat(platform)` **emission watchdog + session stall supervisor +
  classification observability** (#144): `audio_frames_emitted` counts
  both demux paths, per-session `Classification()` and
  `SinceLastEmission()` answer "why is nothing produced", and
  `SessionManager.SetStallWatchdog` recycles wedged sessions (BYE
  first, teardown, optional immediate re-INVITE hook). Closes the
  "six hours of misclassification went unnoticed" class from the
  2026-10-23 pickup incident.
- `fix(platform)` **bare G.711 classification wins over the ES
  heuristic** (#143): on audio-only sessions the first-packet
  classifier now tries the bare-G.711 hint before the H.264 ES
  heuristic — a quiet A-law start (0x55 bytes reading as NAL-21) no
  longer feeds the whole session into the ES demuxer with zero output.
- `fix(platform)` **platform-initiated requests ride the device's
  REGISTER transport** (#145): a TCP-registered device now receives
  INVITE/BYE/MESSAGE/SUBSCRIBE over its pooled TCP connection
  (`;transport=tcp` recipients; observed fix for Hikvision TCP-mode
  INVITEs dying unanswered, MiBeeNvr #1058) — talk/playback/subscribe
  recipients previously carried no transport param at all.
- `feat(platform)`: voice-intercom uplink receive leg (#141) — the § 9.4
  sendrecv offer advertises the talk socket and upstream-capable devices
  stream return PCMA to it; a per-session read loop now drains the socket
  (plain RTP/PCMA validation, SSRC changes tolerated, garbage dropped
  silently), payloads surface via `Server.OnTalkUplink` (fresh copies,
  non-blocking callback), and `TalkStatus` grows `uplink_packets` /
  `uplink_bytes` / `uplink_ssrc` so integrators can prove the return leg
  without wiring a callback. Full-duplex intercom panels build on this.

## [v0.13.0] — 2026-10-16

The audio-channel package (#135, MiBeeNvr #962 prerequisite): network
pickups enroll as audio-only channels (Table E.1 type code 136 — the
standard's own marker in the channel ID), the platform's live INVITE
negotiates a lone m=audio line (G.711 A-law, PT 8), the receiver takes
bare G.711 RTP or PS-muxed audio, and the Annex C audio stream-type
map completes (G.722.1/G.723.1/G.729).

- `feat(platform)` **audio-only channels — network pickups** (issue
  #135, MiBeeNvr #962 prerequisite): a voice-input channel (Table E.1
  type code 136, digits 11-13 of the 20-digit ID) enrolls like a camera
  channel but carries no video track — the standard's own type code is
  the marker, no extra catalog field. `ChannelTypeCode`/`IsAudioInputChannel`
  expose the taxonomy; `RegisterChannel` derives `Channel.TypeCode` and
  `Channel.AudioOnly` from the ID. The live INVITE for such channels
  negotiates a lone `m=audio` line (G.711 A-law, PT 8) across UDP and
  both TCP setups; the receiver classifies non-PS payloads on
  audio-only sessions as bare G.711 RTP (PT 0 → `g711u` frames) while
  PS-muxed audio keeps the demuxer path; the keyframe watchdog exempts
  audio-only sessions (no IDR ever arrives) and the recorder-deferral
  gate keys on the audio writer instead of the NALU one.
- `feat(platform)` **PS audio stream types complete** (Annex C.2.4): the
  demuxer's codec map gains G.722.1 (0x92), G.723.1 (0x93) and G.729
  (0x99) — the three Annex C audio entries that previously demuxed to
  an unlabeled codec and were dropped. `AudioFrame.Codec` widens to
  `g7221`/`g7231`/`g729` alongside `g711a`/`g711u`/`aac`/`svac`.

## [v0.12.0] — 2026-10-14

The transport-parity package, twin of gb28181-rs v0.13.0 — TCP and
SIPS signaling with the full outbound lifecycle (dial → REGISTER →
keepalive → redial), subscriptions and async exchanges over streams,
passive TCP media, multi-MESSAGE pagination, the multi-channel
catalog and five-query host seams, multicast in both roles (platform
IGMP join, device TTL knob), and platform-side ES-over-RTP receive
(RFC 6184/7798). Includes the breaking `DispatchInboundMessage`
paged signature and the gosip upstream data-race fix.

- `feat` **multicast media** (issue #111 follow-up): the platform half
  joins the IGMP group (`ListenMulticastUDP`) when the INVITE's media
  address is a group — one stream reaches every joined viewer; the
  device half sends to group addresses and gained a `multicast_ttl`
  config knob (unix `IP_MULTICAST_TTL`; windows no-op, OS default).
  Previously documented unicast-only.

- `feat(device)` **inbound SIPS listener** (`transport: "tls-listen"`):
  the platform dials the device over TLS — the device's own certificate
  (`tls_cert_file` + `tls_key_file`, required) identifies it;
  connections flow through the shared framed reader with every host
  seam and are registered in the connection table so answers route
  back. Complements the outbound SIPS client (`"tls"`).

- `feat(device)` **host data for the 2022 five information queries**
  (A.2.4.10-14 → A.2.6.12-16): a new `InfoQueryProvider` seam
  (`SetInfoQueryProvider`; types in `infoquery.go`) fills the answers
  with real hardware state — HomePosition, CruiseTrackList,
  CruiseTrack (CruisePointList), PTZPosition (nil fields omitted) and
  SDCardStatus (≤ 8 items). Without a provider the answers stay the
  minimal valid form.

- `feat(device)` **multi-channel catalog** (A.2.1):
  `Config.ExtraChannels` / `DeviceContext.ExtraChannels`
  (`[]ChannelItem`) lists additional channels after the device's own
  entry — the device ID becomes each channel's ParentID, `ON` the
  status default; large lists page across MESSAGEs automatically.

- `feat(device)` **passive TCP media** (Annex C.2, issue #14 follow-up):
  an `a=setup:active` offer (the platform dials the device's media
  port) is answered `a=setup:passive` with a real listening port
  instead of 488 — the live and playback paths accept the platform's
  dial before streaming ($-framing unchanged); teardown closes the
  listener so a pending accept aborts. `buildDeviceSDP` gains the
  setup role argument.

- `feat(device)` **async snapshot/upgrade exchanges on the stream
  transports**: the A.2.5.7 / A.2.5.9 completion notifies leave via
  `sendToPlatform` — the dialed connection on `tcp-out`/`tls`, the UDP
  socket as before on `udp`. Only the inbound `"tcp"` listener mode
  (no tracked platform connection) still rejects.

- `feat(device)` **outbound plain-TCP transport (`transport: "tcp-out"`)**:
  the device dials the platform and runs the full REGISTER/keepalive
  lifecycle over the connection — the same stream-client model as SIPS
  (`"tls"`), minus the handshake; the platform's INVITE/BYE/SUBSCRIBE
  arrive on the same connection. `"tcp"` keeps its inbound-listener
  meaning.
- `fix(device)` **stream transports heal a dropped connection** (tls and
  tcp-out): the read loop's connection-table cleanup used to strand
  every send on "no SIPS connection" forever — the re-registration
  cycle now re-dials first (`ensureStreamConn`), so a platform restart
  or network blip heals on the next cycle. (The startTLSClient comment
  always claimed this; the code never did.)

- `docs` README transport notes refreshed after the TCP-parity waves:
  the device row now states that SUBSCRIBE/NOTIFY dialogs and paged
  catalog/record answers ride any transport, and the §9.4.2 completion
  INFO rides the dialog's own transport (both languages).

- `feat(device)` **subscriptions and NOTIFYs over TCP/TLS dialogs**
  (audit follow-up): a SUBSCRIBE arriving on a TCP/TLS connection now
  books (previously answered a bare 200 without booking) — the
  registry peer generalizes to `net.Addr`, and the subscription's
  NOTIFYs ride the connection through the server's connection table
  (`DeviceNotifier.BindTCP`). UDP subscriptions keep the shared UDP
  socket; unknown-transport peers are answered but not booked. The
  SUBSCRIBE 200 OK also routes via `sendSIP` (both transports).

- `feat(device)` **MediaStatus INFO over TCP/TLS dialogs** (§9.4.2,
  issue #60 follow-up): the playback/download completion INFO rides the
  dialog's own transport — the shared UDP socket for UDP dialogs (as
  before), the INVITE's connection from the server's connection table
  for TCP/TLS dialogs (previously never sent, flagged as a follow-up).
  `mediaEndNotify` carries the dialog transport; the send is
  `sendMediaEnd` (unit-tested over a live connection).

- `feat(device)` **Catalog/RecordInfo pagination** (A.2.6.1/A.2.6.4):
  large answers now page across multiple MESSAGEs — every page carries
  the TOTAL `SumNum` while the list `Num` carries the page's own count
  (platforms accumulate by SN; pages of 10 keep one UDP datagram well
  under the 64 KiB SIP limit, paced 50ms apart). **API**:
  `DispatchInboundMessage` now returns `[]SipMessage` (one entry per
  page). `BuildCatalogResponseMessagePaged` /
  `BuildRecordInfoResponseMessagePaged` join the builder surface; the
  original builders delegate. `DeviceList` gains the standard-required
  `Num` attribute (A.2.6.1), matching the Rust twin's wire form.

- `feat(device)` device-side completeness wave 1 (twin of gb28181-rs
  #86): five gaps surfaced by the full-coverage audit —
  - **PresetQuery response** (A.2.4.8 → the A.2.6 answer):
    `BuildPresetListResponseMessage` emits the wire shape `SumNum` +
    `PresetList Num` + `Item{PresetID,PresetName}`; the dispatch answers
    the valid empty list (no PTZ of our own — the 2022-five stance).
  - **Platform-negotiated registration expiry**: the REGISTER 200 OK's
    `Expires` header (Contact `;expires=` fallback, ≥60s floor) now
    drives the refresh ticker (half the negotiated window; the ticker
    resets when the negotiation changes).
  - **Incoming CANCEL** (UDP + TCP dispatch): answered 200 OK per
    RFC 3261 §9.2 — INVITEs are answered immediately so a CANCEL
    usually races the 200; leaving it unanswered left platforms
    waiting.
  - **Silent-platform detection**: keepalive accounting only reacted
    to send errors and explicit rejections — a platform that stopped
    answering entirely never tripped re-registration. Any SIP response
    stamps a contact clock, and `platformSilent` (heartbeat ×
    timeout_count) triggers re-registration at the same threshold.
  - **Subscription-State honesty**: NOTIFYs advertised a hard-coded
    `expires=3600`; they now carry the subscription's real remaining
    lifetime.

- `docs` multicast explicitly out of scope (#111): the README states
  media distribution is unicast-only on both roles — SDP multicast
  `c=` offers are not joined and the platform never issues an IGMP
  join. Revisit when a real multi-viewer-same-stream scenario shows up
  (per the issue's own trigger criterion).

- `feat(platform)` ES over RTP receive side (#110): GB/T 28181-2022
  allows the media channel to carry bare H.264/H.265 over RTP instead
  of MPEG-PS. The Receiver classifies each session's first payload (PS
  start codes win; NAL-shaped payloads latch ES) and depacketizes RFC
  6184 (single NALU / STAP-A / FU-A) and RFC 7798 (single / AP / FU)
  payloads into the same `[][]byte` AU shape the PS path emits —
  FrameHub consumers and the AU/NALU callbacks do not change. The AU
  boundary stays the RTP marker bit; the loss path drops partial FU
  reassembly (a hole is corruption, not a smaller frame); malformed
  aggregation entries are rejected, not misparsed. ES-only senders
  (the "platform can't pull this device at all" hard failure) now
  interop. The device-side ES *sending* half stays PS-default and is
  not part of this batch (per the issue's own scoping).

- `feat(device)` 2022 closure controls (#108): `DeviceUpgrade`
  (A.2.3.1.12) lands behind a `SetDeviceUpgrader` seam shaped like the
  snapshot executor — the host downloads/flashes, the library sends the
  A.2.5.9 `DeviceUpgradeResult` notify correlated with the request's
  SessionID (reason codes 01 download timeout / 02 package corrupt /
  03 system error / 99 other; upgrader errors report 99).
  `FormatSDCard` (A.2.3.1.13, integer element — 0 formats every card)
  and `PTZPreciseCtrl` (A.2.3.1.11 / A.2.1.11, optional absolute
  Pan/Tilt/Zoom doubles) join the regular `ControlCallbacks`. Platform
  builders: `SendDeviceUpgradeCmd` / `SendFormatSDCardCmd` /
  `SendPTZPreciseCmd`; `manscdp.Decode` parses the
  `DeviceUpgradeResult` notify.

- `feat(device)` DeviceConfig closure family (#109): five more 2022
  sub-commands decode and fire optional host callbacks —
  `VideoParamAttribute` (per-stream codec attributes, A.2.3.2.5),
  `VideoRecordPlan` (weekly schedule, A.2.3.2.6), `VideoAlarmRecord`
  (alarm recording with pre/post roll, A.2.3.2.7), `PictureMask`
  (privacy-mask regions, A.2.3.2.8) and `OSDConfig` (time/text
  overlay, A.2.3.2.11) — wire forms verified against the standard
  text (A.2.1.12-17). No callback keeps the explicit reject. SVAC
  encode/decode configs (A.2.3.2.3/4) are now documented as
  explicitly unsupported; cruise-track configuration is NOT a
  DeviceConfig command in 2022 — it rides the PTZCmd cruise
  instructions (A.3.5, already decoded).
- `feat(device)` ConfigDownload grows the `VideoParamOpt` block
  (A.2.1.20): download speeds + resolutions from the new
  `Config.VideoParamOptDownloadSpeed`/`VideoParamOptResolution`
  ("/"-joined, empty omits the block).

- `fix(manscdp)` snapshot rides the device-config channel (#107): the
  2022 image-snapshot command is a `Control` body with
  `CmdType=DeviceConfig` carrying `SnapShotConfig` — not
  `DeviceControl`/`SnapShot` as previously modeled — matching real 2022
  platform captures (A.2.1.24). `DeviceConfig.SnapShotConfig` replaces
  the removed `DeviceControl.SnapShot` field; the device answers with
  the A.2.6.8 `Result=OK` response before running the exchange, and the
  reject goes back on the same DeviceConfig channel;
  `platform.PTZController.SendSnapShotCmd` emits the corrected body.
  `manscdp.SnapShotCmd` and `device.SnapshotExecutor` keep their names
  and shapes.

## [v0.11.0] — 2026-09-20

The GB/T 28181-2022 device-side closure package (#80/#81): every
platform→device control, config, and intercom flow the standard defines
for the device role now has a decoded wire path and a host seam — plus
two SIP-level race fixes and a hardened CI floor.

- `feat(device)` DeviceControl sub-command decode + host callbacks
  (#85): `IFrameCmd Send`, `RecordCmd Record|StopRecord`, `GuardCmd`,
  `AlarmCmd`, `TeleBoot Boot`, and `PTZCmd` A5 05 passthrough decode
  via `ControlCallbacks`/`SetControlHandlers` — a command with no
  registered callback keeps the historical explicit reject. Platform
  builder `SendIFrameCmd` added alongside.

- `feat(device)` PTZCmd bit-level decode (#89): a total
  `DecodePTZCommand` (length/A5/checksum failures → `PtzInvalid` with
  the raw hex preserved), direction/lens bits, preset, cruise, FI and
  aux switches, driven by a golden table generated from the
  platform-side constructors so encode and decode cannot drift.

- `feat(device)` DragZoom control decode + `OnDragZoom` seam (#92):
  2022 §A.2.3.1.8/9 line format — six required integer children
  (`Length/Width/MidPointX/MidPointY/LengthX/LengthY`); a missing child
  is an explicit reject on both twins.

- `feat` DeviceConfig minimum + ConfigDownload + HomePosition (#90):
  `ConfigCallbacks` (`BasicParam` all-optional children, `FrameMirror`
  0–3, `AlarmReport` switches), minimal `ConfigDownload` response, and
  `OnHomePosition` (DeviceControl family). Also fixes the platform
  `SendDeviceControl` HomePosition wire form — the old string-form body
  never matched A.2.3.1.10.

- `feat(device)` SUBSCRIBE/NOTIFY framework (#87): per-event
  subscription registry (renewal, read-time expiry, CSeq bookkeeping),
  host `DeviceNotifier` (`SendAlarm`/`SendCatalogChange`/
  `SendMobilePosition`, unsubscribed = no-op), `SetPositionSource`
  periodic MobilePosition reporting.

- `feat` §9.4.2 MediaStatus INFO (#86): `BuildMediaStatusInfo` is
  byte-identical with the rust twin; the notification fires only on
  natural session completion — BYE aborts stay silent.

- `feat(device)` voice talkback receive session (#88): `TalkbackSink`
  for G.711 PCMA/PCMU downlink, 488 without a sink, single-dialog slot
  teardown shared across video/playback/talkback, wire goldens pinned
  against real NVR offers.

- `feat(device)` talkback upstream — device→platform G.711 RTP sender
  (#94): `SetTalkbackSource`, 20 ms pacing, PT 8/0; a `recvonly` offer
  without a source is refused 488 and goldens are otherwise unchanged.

- `feat` voice broadcast delivery end-to-end (#96): platform
  `StartBroadcast` (A.2.5.5 notify) → device `followBroadcast`
  (A.2.6.11 ack + audio INVITE back-call with the platform's real
  address as Request-URI) → platform `answerBroadcastInvite` streaming
  20 ms PCMA RTP; device `SetOnBroadcast` replaces the unreachable
  dead API. Pinned by the conformance loopback
  `TestLoopback_VoiceBroadcast`.

- `feat(device)` graceful deregistration helper (#93):
  `Server.Deregister(ctx)` — REGISTER with `Expires: 0`.

- `feat(device)` SIP-Date time sync observation (#91): parses the
  three RFC 3261 Date forms from REGISTER responses; observation only,
  never sets the clock.

- `feat(device)` expose the negotiated upstream talkback law (#98):
  `Server.TalkbackUpstreamCodec()` — the per-offer negotiated PCMA/PCMU
  law for hosts feeding the upstream sender.

- `fix(sip)` ACKs are built from a pre-send INVITE snapshot (#97,
  fixes #95): gosip's transport layer rewrites the top Via of in-flight
  requests without a lock (upstream gosip#87), so the four ACK paths
  now deep-clone the INVITE before handing it to the transaction layer.
  The race detector showed 89 reports with the pass-through variant
  and is clean with the snapshot.

- `test(sip)` probe-silent teardown answers the BYE for real (#99):
  the 3 s answer window was shorter than the probe chain under load,
  leaving the BYE unanswered on gosip Timer F (~32 s). Root fix: 10 s
  window + 45 s Eventually + explicit answerer-exit wait.

- `chore` toolchain floor go1.26.6 (#100): pulls the stdlib security
  fixes (net/url, crypto/tls, net/http, encoding/xml, …); govulncheck
  clean.

- `ci` statements coverage gate at 80% (#101): the test job now counts
  cross-package coverage (`-coverpkg=./...`, measured baseline 83.0%)
  and fails below the floor.

- `test` fuzz targets on every untrusted parse surface (#102): native
  Go fuzz for `device.Parse` (raw SIP datagrams), `manscdp.Decode`
  (MESSAGE bodies incl. the GBK/GB18030 path, invariant: nil error
  always identifies a CmdType), `DecodePTZCommand` (byte-faithful
  totality), `ParseChallenge`, SDP parsing, and `ParseSIPDate`. Seeds
  run on every `go test`; extended local runs: 2M+ execs, zero
  crashes.

## [v0.10.0] — 2026-09-14

- `feat(cascade)` multi-level loop prevention (issue #77, MiBeeNvr #451
  Slice 3): `CameraInfo.OriginDeviceID` names the downstream device a
  channel was learned from (empty = local). Catalog aggregation is now
  per-upper — `catalogItemsFor(u)` excludes channels whose origin equals
  that upper's platform ID (the upper's own channels echoing back through
  its downstream registration), covering query answers, subscription
  NOTIFYs, and registration pushes; INVITEs for excluded channels are
  answered 404 exactly like `CascadeHidden`. Exclusions log so silent
  loops surface. Third-party origins are unaffected.

- `feat(manscdp)` GB/T 28181-2022 information queries (standard foreword
  additions): the five query/response pairs — `HomePositionQuery`,
  `CruiseTrackListQuery`, `CruiseTrackQuery`, `PTZPosition`,
  `SDCardStatus` — join the codec with golden strings pinned to the
  standard text (A.2.4.10-14 / A.2.6.12-16); responses reuse the query
  CmdType under a Response root, as RecordInfo does. The device role
  answers all five with the minimal valid empty-capability responses
  (optional blocks omitted, required SumNum zero) instead of the legacy
  unknown-CmdType warn + silence.
- `feat(sip)` X-GB-Ver protocol-version identification (2022 Annex I):
  `device.Config.ProtocolVersion` / `platform/sip.Config.ProtocolVersion`
  (opt-in, e.g. "3.0") stamp the header on REGISTER and its 401/200
  responses; the peer's version is logged on the platform side and
  exposed via `device.Server.PlatformProtocolVersion()`. Unconfigured,
  the wire form is unchanged.
- `feat(cascade)` X-RoutePath path announcement (2022 Annex H.3):
  `Config.RoutePathAnnounce` (optional 20-digit platform ID) stamps
  `X-RoutePath` on INVITE 200 responses — a middle platform tells the
  upper where the session anchored; incoming `X-PreferredPath` is parsed
  and logged. `platform/sip` exports `ParsePlatformIDList` /
  `FormatPlatformIDList` / `RoutePathHeader`.
- `fix(psmux)` SVAC-audio stream_type corrected to `0x9B` per the 2022
  Annex C table (was `0x81` — a wire bug found by diffing against the
  standard text; golden updated with the correction note). AAC audio
  (`0x0F`) is now settable via `SetAudioCodec("aac")`.
- `test(sip)` seek-phase INFO assertions skip stale resume
  retransmissions (#75) — the second sighting of the
  TestPlaybackControlResumeSeek CI flake is closed by answering
  non-matching bodies with a bare 200 (gosip matches responses by CSeq,
  so the seek transaction still waits for its own answer); the wire
  contract verification is not weakened.

- `feat(cascade)` on-demand main-stream activation (multi-level cascade,
  MiBeeNvr #451): the upper platform's INVITE for a channel whose hub is
  idle — a GB28181 child camera that is not currently recording — now
  asks the host to start the pull through the new `HubActivator` seam
  (`SetHubActivator`, bounded by `Config.HubActivationTimeout`, default
  10s) and answers 200 only once a real hub exists; a failed activation
  keeps the legacy 500 and never establishes a medialess dialog. One
  activated forward holds one reference for its lifetime — release runs
  at session teardown (BYE / supersede / Stop). Without an activator the
  behavior is unchanged.

## [v0.9.0] — 2026-09-10

The device-snapshot capability package (mibee-eye-raspi#28): one
complete user-valuable feature with tests and bilingual docs.

- `feat(device)` snapshot command execution (mibee-eye-raspi#28 / GB/T
  28181-2022 A.2.1.24 + A.2.5.7): a DeviceControl(SnapShot) MESSAGE is
  answered 200, handed to the new `device.SnapshotExecutor` seam
  (`SetSnapshotExecutor`), and completes asynchronously with an
  UploadSnapShotFinished notify echoing the SessionID plus one
  SnapShotFileID per uploaded file — an empty list reports the exchange
  as wholly/partially failed. The executor owns the product side
  (capture + POST each JPEG body to the command's `UploadURL`
  verbatim). Without an executor — or over a non-UDP transport — the
  control is now explicitly rejected (parity with the Rust twin; a
  fast failure for the platform, where previously the body fell
  through parseQueryDual's parse-warn + silence).

## [v0.8.0] — 2026-09-09

The snapshot-event capability package (requested by downstream MiBeeNvr
pin hygiene, issue #66): one complete user-valuable feature with tests
and bilingual docs.

- `feat(sip)` snapshot-finished event (#54): the 2022 UploadSnapShotFinished
  notify (MESSAGE, A.2.5.7) now publishes `gb28181.snapshot.finished` with
  DeviceID, SessionID, and the SnapShotList — hosts close their pending
  snapshot sessions on it; an empty list means the capture or upload
  failed wholly or partly. Event-bus reads now go through
  `eventBusSnapshot()` (subMu) — `SetEventBus` after Start no longer
  races gosip handler goroutines; the alarm publish path got the same
  fix. Bilingual topic table in docs/en/platform.md + docs/zh/platform.md.

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
