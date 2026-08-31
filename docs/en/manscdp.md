# MANSCDP XML codec

The `manscdp` package is the shared MESSAGE-layer codec used by both
roles: typed structs for every command, XML marshal/unmarshal, and the
charset reality of GB/T 28181 deployments.

## Message types

| Type | Direction | Purpose |
|---|---|---|
| `Catalog` / `Item` | device→platform | catalog response (channels) |
| `CatalogQuery` / `CatalogNotify` | both | catalog request / subscription update |
| `Keepalive` | device→platform | heartbeat |
| `DeviceInfo` | device→platform | manufacturer/model/firmware |
| `DeviceStatus` | device→platform | online/record/alarm status |
| `RecordInfo` / `RecordItem` / `RecordInfoQuery` | both | recording index query/response |
| `DeviceControl` | platform→device | PTZ / alarm reset commands |
| `Alarm` | device→platform | alarm notification |
| `Broadcast` | both | voice broadcast |
| `TimeSyncQuery` / `TimeSyncResponse` | both | time synchronization |

Plain `encoding/xml` structs — marshal/unmarshal directly, or use the
helpers the `device`/`platform` packages build on them.

## Dual form: element vs attribute

Real platforms disagree on whether MANSCDP fields are child elements
(`\<Name>x\</Name>`) or XML attributes (`Name="x"`). The codec emits
and accepts **both**; the device role's wire bytes are pinned by
golden tests shared with the Rust twin, so interop-critical senders
must not be reimplemented ad hoc.

## Charsets

Wire bodies arrive as UTF-8 **or** GB2312/GBK/GB18030 depending on the
platform. Decode inbound bytes before XML parsing:

```go
import "github.com/mickeyzzc/gb28181-go/manscdp"

utf8, err := manscdp.CharsetDecode(rawBody)
```

## Two type sets, deliberately

`device/` also carries a private MANSCDP type set whose serialized
bytes are golden-tested byte-exact against the Rust twin
(`gb28181-rs`). Until a wire-compat strategy for consolidation exists,
`manscdp/` is the shared package for new platform-side code, and the
`device/` set stays frozen for twin parity — see the README's
"API surface note".
