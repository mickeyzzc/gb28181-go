# The GB/T 28181 full chain, no hardware attached (interop lab)

No real camera, no GB/T 28181 platform available — but you need to verify
a protocol stack, hand a platform team a test peer, or simply watch
"register → keepalive → catalog → INVITE → RTP → BYE" run end to end.
That is what this lab does. Every tool ships prebuilt with each
[release](https://github.com/mickeyzzc/gb28181-go/releases) (seven
platforms, `CGO_ENABLED=0` static, zero dependencies), or run from
source. Full flag reference per subcommand:
[the interop tools manual](gb28181-go-tools.md).

## 0. Install (pick one)

```bash
# 1) Prebuilt bundle (recommended: copy to any Linux/Windows/macOS box)
gh release download v0.15.0 -R mickeyzzc/gb28181-go \
  -p 'gb28181-cli_linux_amd64.tar.gz' && tar xzf gb28181-cli_linux_amd64.tar.gz

# 2) Version-pinned install
go install github.com/mickeyzzc/gb28181-go/cmd/gb28181@v0.15.0

# 3) From a checkout
go run ./cmd/gb28181 <subcommand>
```

The defaults interlock (device ID / channel ID / domain / server ID /
digest realm are pre-paired), so the commands below talk to each other
with no flags beyond the password.

## 1. Terminal one: start a minimal platform

```bash
gb28181 platform-uas -listen :5060 -password secret
```

This is a real UAS: SIP listener, digest auth, keepalive liveness window,
catalog handling, alarm `EventBus` subscription printed to stdout. It
waits silently for a device to show up.

## 2. Terminal two: register the virtual camera

```bash
gb28181 device-register -platform 127.0.0.1:5060 -password secret
```

It runs until you Ctrl-C it (registration refresh + keepalives + query
answers). If local port 5060 is already taken by another SIP service
(`bind: address already in use`), add `-local-sip-port 5061` — the
virtual camera is the calling party, so the local port does not affect
interoperability.

Both terminals now show the same rhythm:

1. **REGISTER → 401 (digest challenge) → REGISTER with Authorization → 200**;
2. **Keepalive**: a Keepalive MESSAGE every 60s answered with 200 (the
   liveness window is 60s by default, `-keepalive-window`);
3. **Catalog**: the platform's catalog query gets the device's own
   channel back in a Catalog response.

At this point the signaling leg (registration, auth, keepalive, catalog)
is alive between two processes — that is the platform developer's virtual
camera: point `-platform` at any real platform's SIP address for
integration runs and regression, including `-transport tcp` and
`tls`/SIPS variants.

## 3. The media leg + full-chain proof (one command)

The two simulated subcommands are signaling plus a ready frame source
(`device-register` pushes synthetic frames — 1 IDR + 1 P frame per
second — once INVITEd; `platform-uas` shows the INVITE host seam,
`InviteChannel`/`ByeChannel`, as a comment block in
[main.go](../../cmd/gb28181/platform_uas.go)). To drive the **full chain
including media** in one shot, the repository ships the conformance
loopback suite — a real `device.Server` against a real
`platform/sip.Server`, where every test is a full-chain script:

```bash
go test ./conformance/ -v
```

- `TestLoopback_RegisterCatalogKeepalive` — register+digest, catalog,
  keepalive liveness;
- `TestLoopback_InviteRoundTripBYE` — INVITE → **byte-exact RTP/PS
  round-trip** (device psmux push → platform reassembly → psdemux) →
  BYE teardown;
- `TestLoopback_SIPSRegisterCatalog` — the TLS-signaling (SIPS) variant;
- `TestLoopback_VoiceBroadcast` — the full voice-broadcast flow;
- `TestSoakInviteCycles` — INVITE soak cycles (session setup/teardown
  stability).

What makes this suite worth more than single-side fake-socket tests:
**the UAC and UAS implementations must agree on one protocol reading** —
field encodings, branch/CSeq discipline, SDP shapes — inside one
repository, on every CI run. "Developer self-attestation" means exactly
this: run the command and keep the output in your integration records.

## 4. Media path alone

```bash
gb28181 psmux-rtp
```

No listener, no peer: synthetic H.264 → MPEG-PS → RTP packetization →
in-process UDP receiver → statistics every 50 packets. The binary smoke
test for a new architecture or platform, and the minimal reproducer for
media-path issues.

## 5. Common variants

| Scenario | Command change |
|---|---|
| Platform on another host | `-platform 192.168.1.10:5060` |
| TCP signaling | `-transport tcp` on the device; the platform must listen on TCP — the CLI exposes no transport flag, the library seam is `platform/sip.Config.SIPTransport` |
| SIPS (TLS, GB/T 28181-2022) | `device-register -transport tls` against a SIPS-capable platform |
| Different device identity | `-device-id` / `-channel-id` / `-domain` / `-name` |
| Legacy SM-crypto platforms | Not via the CLI — GB35114 rides the library seams, see the [GB35114 manual](gb28181-go-gb35114.md) |
| Long-run stability | Tune `-register-interval` / `-heartbeat-interval`, pair with `TestSoakInviteCycles` |

## 6. Troubleshooting

| Symptom | Cause and fix |
|---|---|
| REGISTER loops on 401 | The two `-password` values differ; the digest secret must match what the platform provisioned |
| Device registered but no catalog on the platform | The platform never sent a Catalog query, or does not accumulate paged answers (large catalogs split across MESSAGEs, keyed by SN) |
| No replies at all on the device side | Platform address/port unreachable (`-platform` host:port, a firewall blocking UDP 5060) |
| INVITE but no media | The platform's SDP offer must carry `y=` (SSRC); check whether the `-media-ports` range is firewall-blocked |
| TLS/SIPS handshake fails | Certificate trust chain on the platform side; `tls` mode requires a SIPS-capable peer |
| `psmux-rtp` prints zero stats | Binary/architecture mismatch (fetch the matching `gb28181-cli_<os>_<arch>` bundle) |

## 7. Where to go next

- Replace the synthetic frames with your encoder's output: read
  [device_register.go](../../cmd/gb28181/device_register.go) — it doubles
  as the minimal `device` integration sample; swap the frame source for
  a `device.FrameHub` and you are in production wiring;
- Platform-side persistence and events: the `DeviceStore` seam and
  `EventBus` topics are covered in the [platform manual](gb28181-go-platform.md);
- SM-crypto scenarios: the [GB35114 manual](gb28181-go-gb35114.md).
