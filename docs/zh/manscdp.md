# MANSCDP XML 编解码

`manscdp` 包是两个角色共用的 MESSAGE 层编解码：每个命令的类型化
结构体、XML 序列化/反序列化，以及 GB/T 28181 现场的字符集现实。

## 消息类型

| 类型 | 方向 | 用途 |
|---|---|---|
| `Catalog` / `Item` | 设备→平台 | 目录应答（通道） |
| `CatalogQuery` / `CatalogNotify` | 双向 | 目录请求 / 订阅更新 |
| `Keepalive` | 设备→平台 | 心跳保活 |
| `DeviceInfo` | 设备→平台 | 厂商/型号/固件 |
| `DeviceStatus` | 设备→平台 | 在线/录像/告警状态 |
| `RecordInfo` / `RecordItem` / `RecordInfoQuery` | 双向 | 录像索引查询/应答 |
| `DeviceControl` | 平台→设备 | PTZ / 告警复位命令 |
| `Alarm` | 设备→平台 | 告警通知 |
| `Broadcast` | 双向 | 语音广播 |
| `TimeSyncQuery` / `TimeSyncResponse` | 双向 | 时间同步 |

普通 `encoding/xml` 结构体——直接序列化/反序列化，或使用
`device`/`platform` 包在其上构建的辅助函数。

## 双形态：元素式与属性式

真实平台对 MANSCDP 字段该用子元素（`\<Name>x\</Name>`）还是 XML
属性（`Name="x"`）各有坚持。编解码**两种都发得出、都收得了**；设备
侧的线上字节由与 Rust 孪生库共享的金串测试钉死，互操作关键的发送
路径不要自行另写一套。

## 字符集

线上报文依平台不同可能是 UTF-8 **或** GB2312/GBK/GB18030。入站字节
先解码再解析 XML：

```go
import "github.com/mickeyzzc/gb28181-go/manscdp"

utf8, err := manscdp.CharsetDecode(rawBody)
```

## 两套类型集，有意为之

`device/` 还带一套私有 MANSCDP 类型，其序列化字节与 Rust 孪生库
（`gb28181-rs`）逐字节金串对齐。在合并的线上兼容策略出现之前，
`manscdp/` 是平台侧新代码的共享包，`device/` 那套为孪生对齐而冻结
——见 README 的"API 面说明"。
