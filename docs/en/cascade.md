# Cascade client: registering to an upper platform

`platform/cascade` makes *your* platform a LOWER platform in a GB/T
28181 cascade: it registers upward, uploads an aggregated catalog,
forwards INVITEs as streams, and serves playback from your recordings.

## Config

```go
cfg := cascade.Config{
    Enabled:       true,
    ServerDomain:  "34020000002000000001", // upper platform's GB ID
    ServerAddr:    "192.0.2.20:5060",      // upper platform's SIP address
    LocalDeviceID: "34020000002000000002", // THIS platform's ID upward
    Realm:         "3402000000",
    Password:      "secret",
    SIPListen:     ":5061",                // cascade client's own SIP port
    UserAgent:     "",                     // "" = "gb28181-go/cascade"
}
```

Catalog identity defaults are neutral (`Unknown` manufacturer/model)
and overridable via `CatalogDefaultManufacturer` /
`CatalogDefaultModel` — your cascade advertises your brand, not the
library's.

## Host seams — all injected

```go
type CameraSource interface {
    Cameras() []CameraInfo          // your local camera list
    Hub(cameraID string) *platform.FrameHub // live frames per camera
}

type Store interface {
    UpsertCascadeChannel(ctx, CascadeChannel) error
    ListCascadeChannels(ctx) ([]CascadeChannel, error)
    ListRecordings(ctx, RecordingFilter) ([]Recording, error)
}
```

Wire and start:

```go
svc := cascade.New(cfg, cameraSource, store)
// Optional: on-demand sub-stream tier
svc.SetSubStreamAcquirer(subAcquirer)
// Playback from recorded segments:
svc.SetSegmentParser(segmentParser)
svc.Start(ctx)
```

`SegmentParser` feeds playback from your recording pipeline: it
yields `SegmentInfo` (codec + parameter sets + timestamped samples)
per segment file; hosts with an fMP4 pipeline adapt their parser with
a thin wrapper (field names already match).

## What the upper platform sees

- **Catalog** — your cameras as channels, with **stable first-seen
  channel IDs** (upward identity survives restarts).
- **Live** — INVITEs from above subscribe your `FrameHub`, flow
  through `psmux`, and push RTP/PS upward (UDP or TCP).
- **Playback** — RecordInfo from your `Store`; playback INVITEs pump
  recorded segments through the same media path.
- **Signaling** — BYE/SUBSCRIBE/MESSAGE/INFO/OPTIONS all answered.

Multiple upper platforms are supported (`upstreams` config); each is
an independent registration session over the shared listener.
