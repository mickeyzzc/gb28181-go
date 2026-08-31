# 设备端（UAC）：把摄像头注册到平台

`device` 包把一个 Go 程序变成 GB/T 28181 摄像头：带 digest 认证的
REGISTER、保活、目录应答、INVITE 驱动的 RTP/PS 推流——走 UDP、TCP
或 SIPS（TLS）。

## Config 参考

```go
cfg := device.Config{
    Enabled:               true,
    PlatformSIPAddress:    "192.0.2.10",
    PlatformSIPPort:       5060,
    DeviceID:              "34020000001320000001",
    ChannelID:             "34020000001310000001",
    SIPDomain:             "3402000000",
    Password:              "secret",
    LocalSIPPort:          5060,
    RegisterIntervalSecs:   60,
    HeartbeatIntervalSecs: 60,
    HeartbeatTimeoutCount: 3,
    Transport:             "udp", // "udp" | "tcp" | "tls"
}
```

YAML tag 一致（`platform_sip_address`……）——结构体可以直接从应用的
`gb28181:` 配置节反序列化。

`Transport: "tls"`（SIPS，GB/T 28181-2022 A 级）时还要配置 TLS 字段：
`TLSCAFile`（平台自签证书或 CA）、可选 `TLSCertFile`/`TLSKeyFile`
（双向认证）、`TLSInsecureSkipVerify`（仅限实验室）。国标惯例是自签
CA、序列号即设备/平台 ID。

## 身份

```go
srv := device.New(cfg, device.DeviceInfo{
    Name:         "前门",
    Manufacturer: "Acme",
    Model:        "Cam-X",
    Firmware:     "1.2.3",
    HardwareID:   "CamX-SoC",
    SerialNumber: "SN-42",
}, hub)
```

`device.UserAgent`（包级变量，默认 `GB28181-Go/1.0`）盖 SIP
User-Agent——在 `New`/`Start` **之前**赋你自己的值；之后并发改会与
报文构造器竞态。

## 直播帧：FrameSource

```go
type FrameSource interface {
    Subscribe(ctx context.Context) *FrameSubscription
    Unsubscribe(id string)
}

type FrameSubscription struct {
    ID      string
    Channel <-chan AccessUnit
}
```

把 `AccessUnit`（不含 Annex-B 起始码的 NAL、`Timestamp`、
`KeyFrame`）推进你的 hub；服务器在 INVITE 到来时订阅、向平台媒体
端口推 RTP/PS。期望有界、写满即丢的 channel——绝不让编码器被慢消费
者阻塞。`device.NewFrameHub()` 是现成实现；`SIPPort()`/
`SIPTCPPort()` 报告实际绑定端口。

## 录像：RecordingIndex

```go
srv.SetRecordingIndex(myIndex) // 实现 Lookup(startMs, endMs) []SegmentMeta
```

挂上索引后，服务器应答 RecordInfo 查询、提供按节奏回放与全速下载，
包括 SIP INFO 回放控制（暂停/恢复/拖动/倍速）。录像段用
`device.OpenSegment` 读取的参考格式：Annex-B 裸 H.264 + 每帧
`.ts.jsonl` 边车。

## 生命周期

```go
err := srv.Start(ctx) // 注册/保活/推流 goroutine 都挂在 ctx 取消下
...
srv.Stop()
```

注册失败带退避重试，到期自动重注册。

## 设备 ID

```go
id, err := device.FormatDeviceID("34020000", 0, device.DeviceTypeIPC, 42)
parts, err := device.ParseDeviceID(id) // 中心/行业/类型/序号
```

20 位编码：`[8 位行政区划][2 位行业][3 位类型][7 位序号]`。类型常量
含 `DeviceTypeIPC`（111）、`DeviceTypeNVR`（118）、
`DeviceTypeAlarm`（122）。
