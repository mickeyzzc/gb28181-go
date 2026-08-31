# PS muxer and RTP packetization

The `psmux` package muxes H.264/H.265 (+ G.711 audio) into MPEG-2
Program Stream and packetizes it onto RTP — shared by the device push
path and platform/cascade forwarding.

## Muxer

```go
import "github.com/mickeyzzc/gb28181-go/psmux"

m := psmux.New()
m.SetVideoCodec("h264") // or "h265"
m.SetAudioCodec("pcma") // optional G.711

// WriteAU takes a full Annex-B access unit (with start codes).
ps := m.WriteAU(annexBAU, pts, isIDR)
audioPS := m.WriteAudio(g711Frame, pts)
```

- IDR access units emit pack header + system header + PSM before the
  PES; P-frames emit pack header + PES.
- Access units over the PES length limit are split into bounded PES
  packets automatically.

## RTP packetization

```go
// UDP push:
p := psmux.NewRTPPacketizer(conn, udpAddr, ssrc, initialSeq)
err := p.Send(ps, tsTicks)

// TCP-passive media (GB28181 TCP mode):
p := psmux.NewRTPPacketizerTCP(conn, ssrc, initialSeq)
```

`Send` fragments oversized PS into multiple RTP packets with correct
markers; `Sent()` reports the packet count.

## Which muxer do I use?

Two muxers coexist **deliberately**:

- `device.MuxH264ToPS` — H.264-only, battle-tested against real
  platforms, byte-golden-tested against the Rust twin. Use when you
  need twin-identical bytes.
- `psmux.New()` — H.265 + G.711 audio capable, used by platform
  forwarding and cascade. Use for new integrations needing
  H.265/audio.

See the README's "API surface note" for why consolidation is deferred.

## nalutil

`nalutil` answers the questions both roles ask of NAL streams: is this
AU an IDR, extract SPS/PPS(/VPS), compare parameter sets across
restarts (encoder restart detection).
