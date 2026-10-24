# Interop CLI: gb28181 (device-register / platform-uas / psmux-rtp)

`cmd/gb28181` is the gb28181-go library's **single interop CLI** — one
binary, three subcommands (unified at v0.14.0; previously three separate
example tools whose configuration was hard-coded in source, which made
the prebuilt binaries unusable). Everything is flag-driven.
Every [release](https://github.com/mickeyzzc/gb28181-go/releases) ships
it as a prebuilt zero-dependency bundle
(`gb28181-cli_<os>_<arch>.tar.gz`, `.zip` on Windows, plus
`SHA256SUMS`) for seven platforms: linux amd64/arm64/arm, darwin
amd64/arm64, windows amd64/arm64 — built with `CGO_ENABLED=0`, static;
unpack and run on a Pi, a NAS, or a test laptop, no Go toolchain
needed.

## Which subcommand for which job?

| You want to… | Run |
|---|---|
| Exercise a GB/T 28181 platform's device-facing behavior without a real camera | `gb28181 device-register` |
| Give a device / cascade client a minimal platform to register and pull from | `gb28181 platform-uas` |
| Sanity-check the PS-mux + RTP media path (the same code path the device role uses for live push) | `gb28181 psmux-rtp` |
| See how the pieces integrate (each subcommand file is a runnable minimal integration) | all three sources |

Division of labor with the [onvif-go CLI](https://github.com/mickeyzzc/onvif-go/blob/main/docs/en-US/onvif-go-cli.md): those are
**operations/diagnostics** subcommands (discover, quick-check, deep
diagnostics, virtual camera); these are **protocol-role emulators** —
each library ships tools for its own stack.

## Install

Prebuilt bundle from a release:

```bash
gh release download v0.14.0 -R mickeyzzc/gb28181-go \
  -p 'gb28181-cli_linux_amd64.tar.gz'
tar xzf gb28181-cli_linux_arm64.tar.gz   # the single gb28181 binary inside
sha256sum -c SHA256SUMS                        # verify
```

Or install from source, version-anchored:

```bash
go install github.com/mickeyzzc/gb28181-go/cmd/gb28181@v0.14.0
```

Or from a checkout: `go run ./cmd/gb28181 <subcommand>`.

## device-register — the device (UAC) emulator

A "virtual camera": REGISTERs with the platform (digest auth) →
keepalives on a cycle → answers Catalog / DeviceInfo queries → once the
platform INVITEs, pushes RTP/PS at a **1 IDR + 1 P frame per second**
cadence (synthetic frames; no capture hardware).

```bash
gb28181 device-register \
  -platform 192.168.1.10:5060 \
  -device-id 34020000001320000001 \
  -channel-id 34020000001310000001 \
  -password <digest secret agreed with the platform> \
  -transport udp        # udp | tcp | tls (SIPS, GB/T 28181-2022)
```

More flags: `-domain` (defaults to the ID's first 10 digits),
`-local-sip-port` (5060), `-name` (DeviceInfo name),
`-register-interval` / `-heartbeat-interval`.

Typical uses: platform-side joint testing and regression (catalog
pagination, INVITE, TCP/SIPS transports). It is also the minimal
integration reference for the `device` package — swap the synthetic
frame loop for your encoder output feeding `device.FrameHub` and you
have production wiring.

## platform-uas — the minimal platform (UAS) server

The half devices register into: a SIP listener with digest auth,
keepalive liveness window, catalog handling, INVITE-driven streaming
into a FrameHub fan-out, and alarms surfacing through an `EventBus`
subscription. Device/channel persistence is the `DeviceStore` seam —
the CLI ships an in-memory implementation; a real host writes through
to its database there.

```bash
gb28181 platform-uas -listen :5060 -password <digest secret devices must present>
# optional: -server-id 34020000002000000001 -realm 3402000000
#           -media-ports 30000-30050 -keepalive-window 60
```

Typical uses: give `device-register`, the cascade client, or your own
device implementation a live counterpart — no real platform needed
when exercising device-side behavior.

## psmux-rtp — the media-path self-check

A fully self-contained loopback: muxes synthetic H.264 access units
into MPEG-PS, packetizes as RTP (PT 96) toward an in-process UDP
receiver, and prints packet/byte counters every 50 packets by default
(`-report-every` to tune). It listens on nothing and needs no
counterpart — one command proves the `psmux` muxing and RTP
packetization path works on the current platform (the device role's
live push and the cascade role's stream forwarding both ride it).

Typical uses: binary smoke-test on a new arch/OS; the minimal
reproducer when chasing media-path issues.

## Common notes

- `-password` is **required** on both emulator subcommands (no default
  — an accidentally-shipped placeholder must not read as a library
  bug).
- The subcommand files are the integration samples: reading
  `cmd/gb28181/*.go` reads exactly like an example.
- The tools evolve with releases; per-version deltas live in the repo
  [CHANGELOG](https://github.com/mickeyzzc/gb28181-go/blob/main/CHANGELOG.md).
