[中文](README.zh-CN.md) | **English**

# gb28181-go

[![CI](https://github.com/mickeyzzc/gb28181-go/actions/workflows/ci.yml/badge.svg)](https://github.com/mickeyzzc/gb28181-go/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
![Language: Go](https://img.shields.io/badge/language-Go-00ADD8.svg)
![Tests](https://img.shields.io/badge/tests-89%2B%20passing-brightgreen.svg)

**GB/T 28181-2016/2022 Go 库族** —— 中国国标视频监控的设备端（UAC）与平台端（UAS）双角色。

手写 SIP（设备端不依赖任何 SIP 框架；平台端规划构建于 [ghettovoice/gosip](https://github.com/ghettovoice/gosip) 之上）、MANSCDP XML 编解码、RTP/PS 媒体与会话编排。代码从 Mi-Bee Studio 生产实现逐字抽取，经真实国标平台打磨（摘要 qop=auth、Via branch 唯一性、本机 IP 探测、MANSCDP 属性形式、SIP over TCP、TCP 媒体）。

## 包

| 包 | 角色 | 来源 |
|---|---|---|
| `device/` | **UAC** —— 摄像头注册到 SIP 平台：REGISTER + 摘要认证、目录/设备信息/保活、INVITE 驱动的 RTP/PS 直播、RecordInfo、按帧节奏的回放/下载与 SIP INFO 控制。UDP 与 TCP。 | `mibee-eye-raspi-go` `internal/gb28181` |
| `manscdp/` | 共享 MANSCDP XML 编解码（元素+属性双形式，GB2312/GBK/GB18030/UTF-8） | MiBeeNvr `internal/gb28181/manscdp` |
| `platform/` | **UAS**（迁移批次 1/4 已入）—— 设备/通道注册表（保活离线判定）、MPEG-PS 解复用（含 PSM-less 音频回退启发式）、端口池、PTZ/A505 指令构建。RTP 收流/会话编排/SIP UAS 服务器随后批次迁入。 | MiBeeNvr `internal/gb28181` |
| `psmux/` | PS/RTP 打包 muxer（H.264/H.265、G.711 音频、>60KB AU 分段、RTP 分片），设备端推流与平台/级联转发共用 | MiBeeNvr `internal/gb28181/psmux` |

## 使用（设备端）

```go
import "github.com/mickeyzzc/gb28181-go/device"

cfg := device.Config{
    PlatformSIPAddress: "192.168.1.10",
    PlatformSIPPort:    5060,
    DeviceID:           "34020000001320000001",
    ChannelID:          "34020000001310000001",
    SIPDomain:          "3402000000",
    Password:           "12345678",
}

srv := device.New(cfg, device.DeviceInfo{Name: "My Cam"}, frameSource)
// frameSource 在你的采集帧中心上实现 device.FrameSource；
// 可选 srv.SetRecordingIndex(...) 以支持 RecordInfo/回放。
err := srv.Start(ctx)
```

宿主接缝（接口，宿主注入）：

- `FrameSource` —— 直播 H.264 访问单元
- `RecordingIndex` / `PlaybackIndex` —— 录像段索引（RecordInfo/回放）
- `Config` / `DeviceInfo` —— 连接配置与设备身份（YAML 形状与来源项目一致）

录像段使用 `device.OpenSegment` 读取的参考格式：裸 Annex-B H.264 + 每帧 `.ts.jsonl` sidecar。测试可使用现成的 `device.FrameHub`（有界通道、满则丢弃语义的 `FrameSource` 实现）。

## 开发

本项目严格执行 **TDD**，见 [CONTRIBUTING.md](CONTRIBUTING.md)。CI 强制 `gofmt`、`go vet`、`go test -race`；`main` 分支受保护（仅 PR 合入，CI 必过）。

## 状态

`device/` 与 `manscdp/` 已发布；`platform/` 抽取批次 1/4 已入库（PS 解复用/复用、注册表、端口池、PTZ）。API 面趋于稳定但尚未冻结。在 [Mi-Bee Studio](https://github.com/Mi-Bee-Studio) 每日对 MiBee NVR 国标平台生产验证。

## 许可

MIT —— 见 [LICENSE](LICENSE)。
