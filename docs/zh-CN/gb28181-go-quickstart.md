# 快速开始：把摄像头注册到平台

`device` 包让一个 Go 程序在十分钟内变成 GB/T 28181 摄像头：注册到平台、
应答心跳与目录、在 INVITE 到来时推 RTP/PS。本页给最小可编译示例，
各模块细节见后续章节。

## 安装

```bash
go get github.com/mickeyzzc/gb28181-go@v0.11.0
```

## 最小示例

```go
package main

import (
	"context"
	"log"

	"github.com/mickeyzzc/gb28181-go/device"
)

func main() {
	hub := device.NewFrameHub()
	srv := device.New(device.Config{
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
		Transport:             "udp",
	}, device.DeviceInfo{
		Name:         "Front gate",
		Manufacturer: "Acme",
		Model:        "Cam-X",
		Firmware:     "1.2.3",
		HardwareID:   "SoC",
		SerialNumber: "SN-42",
	}, hub)

	if err := srv.Start(context.Background()); err != nil {
		log.Fatal(err)
	}
	defer srv.Stop()
	select {} // 阻塞；在采集循环里把 AccessUnit 推入 hub
}
```

要点：

- `Start` 完成 REGISTER 摘要认证与保活循环，`Stop` 干净收尾。
- 平台 INVITE 点播时，服务器向 `hub` 订阅实时帧并推 RTP/PS——采集管线
  只管往 hub 推 `AccessUnit`（NAL 不带起始码）。
- 示例值均为文档保留段：`192.0.2.x` 地址、`3402000000…` 段国标 ID。

## 下一步

- [设备端手册](gb28181-go-device.md)：配置全表、TLS（SIPS）、录像索引、设备 ID。
- [平台端](gb28181-go-platform.md)与[级联](gb28181-go-cascade.md)：站在平台一侧的用法。
- [MANSCDP](gb28181-go-manscdp.md)与[PS 封装](gb28181-go-psmux.md)：消息与媒体面细节。
