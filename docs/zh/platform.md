# 平台端（UAS）：SIP 服务器一侧

`platform/sip` 是一个 GB/T 28181 平台：设备向**你**注册。基于
[gosip] 构建，处理 REGISTER/保活/目录/INVITE 全生命周期；持久化
由你注入，事件由你消费。

## Config

```go
cfg := sip.Config{
    Enabled:               true,
    SIPListen:             ":5060",
    ServerID:              "34020000002000000001",
    Realm:                 "3402000000",
    Password:              "secret",
    UserAgent:             "",                  // "" = "gb28181-go"
    InviteResponseTimeout: "32s",               // Go 时长字符串
    PortRange:             "30000-30050",       // RTP 媒体端口池
    AllowedDeviceIDs:      nil,                 // nil = 允许任意设备
}
```

`UserAgent` 是中性化覆盖点——默认值不带产品品牌；上游指纹检查
UA 时才需要设。`InviteResponseTimeout` 界定 `InviteChannel` 等设备
INVITE 应答的上限。

## 持久化：DeviceStore

数据库归你；服务器调用：

```go
type DeviceStore interface {
    UpsertGB28181Device(ctx, GB28181Device) error
    UpsertGB28181Channel(ctx, GB28181Channel) error
    ListGB28181Devices(ctx) ([]GB28181Device, error)
    ListGB28181Channels(ctx, deviceID string) ([]GB28181Channel, error)
    MarkDeviceOffline(ctx, id) error
    BindChannelCamera(ctx, channelID, cameraID) error
    GetGB28181Device(ctx, id) (*GB28181Device, error)
    DeleteGB28181Device(ctx, id) error
    DeleteGB28181Channel(ctx, channelID) error
}
```

测试与 `examples/platform-uas` 演示自带内存实现。

## 事件：EventBus

```go
bus := sip.NewEventBus(64) // 有损:慢消费者丢事件,绝不阻塞
// 订阅设备上/下线、告警、注册事件
```

总线刻意有损——事件是通知，不是队列。

## 会话与活性

- 注册经 digest 挑战（`qop=auth`）；保活 MESSAGE 驱动活性——静默
  超时即经 `MarkDeviceOffline` 标记离线。
- INVITE/BYE 会话走 `SessionManager`，`platform.FrameHub` 扇出给
  多个观看者。
- 接收路径：RTP 重组（UDP 与 TCP 被动）、抖动缓冲、SSRC 锁定、
  MPEG-PS 解复用回 NAL。

## 真机怪癖补丁

服务器带着从真实设备学来的补丁：投机 ACK、对话框重置、REGISTER
源端口轮换、长 GOP IDR 看门狗。它们按检测到的行为激活，不靠配置。

## 一致性

`conformance/` 在本机把真实 `device.Server` 对上真实平台 SIP 服务
器——REGISTER+digest → 目录 → 保活 → INVITE 点播 → 逐字节 RTP/PS
往返 → BYE——每次 CI 都跑，含 SIPS（TLS）变体。改了任一侧，这套
件是裁判。

[gosip]: https://github.com/ghettovoice/gosip
