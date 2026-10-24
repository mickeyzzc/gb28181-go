# 互操作 CLI：gb28181（device-register / platform-uas / psmux-rtp）

`cmd/gb28181` 是 gb28181-go 库的**单一互操作命令行**——一个二进制、
三个子命令（v0.14.0 起合并；此前为三个独立示例工具，且配置硬编码在
源码里，预编译包拿回来根本没法用）。全部配置走旗标。
每次 [release](https://github.com/mickeyzzc/gb28181-go/releases)
以预编译零依赖包发布（`gb28181-cli_<os>_<arch>.tar.gz`，Windows 为
`.zip`，附 `SHA256SUMS`）：linux amd64/arm64/arm、darwin amd64/arm64、
windows amd64/arm64——`CGO_ENABLED=0` 纯静态，拷到树莓派、NAS、测试
笔电上即用，不需要 Go 工具链。

## 哪个子命令干哪件事？

| 你想…… | 运行 |
|---|---|
| 在没有真摄像头的情况下验证一个 GB/T 28181 平台的设备侧行为 | `gb28181 device-register` |
| 给设备端 / 级联端一个可注册、可拉流的最小平台 | `gb28181 platform-uas` |
| 快速验证 PS 复用 + RTP 打包的媒体路径（与设备直播推流同一条代码路径） | `gb28181 psmux-rtp` |
| 看三方怎么集成（每个子命令文件就是一份能跑的最小集成样例） | 三个源码都是 |

与 [onvif-go CLI 工具](https://github.com/mickeyzzc/onvif-go/blob/main/docs/zh-CN/onvif-go-cli.md) 的分工：那边是**运维/诊断**
子命令（发现、体检、诊断、虚拟相机），这边是**协议角色模拟**——两个
库各自面向自己的协议栈。

## 安装

从 release 取预编译包：

```bash
gh release download v0.14.0 -R mickeyzzc/gb28181-go \
  -p 'gb28181-cli_linux_amd64.tar.gz'
tar xzf gb28181-cli_linux_arm64.tar.gz   # 里面就是单个 gb28181 二进制
sha256sum -c SHA256SUMS                        # 校验
```

或按版本锚定从源码装：

```bash
go install github.com/mickeyzzc/gb28181-go/cmd/gb28181@v0.14.0
```

或检出后直接 `go run ./cmd/gb28181 <子命令>`。

## device-register —— 设备端（UAC）模拟器

一台"虚拟摄像头"：向平台 REGISTER（digest 认证）→ 周期保活 →
应答 Catalog / DeviceInfo 查询 → 平台 INVITE 后按 **1 IDR + 1 P 帧/秒**
的节奏推 RTP/PS 流（合成帧，无需采集设备）。

```bash
gb28181 device-register \
  -platform 192.168.1.10:5060 \
  -device-id 34020000001320000001 \
  -channel-id 34020000001310000001 \
  -password <与平台约定的摘要口令> \
  -transport udp        # udp | tcp | tls（SIPS，GB/T 28181-2022）
```

其余旗标：`-domain`（默认取 ID 前 10 位）、`-local-sip-port`（5060）、
`-name`（DeviceInfo 名称）、`-register-interval` / `-heartbeat-interval`。

典型用途：平台侧联调与回归（目录分页、INVITE、TCP/SIPS 传输）；
也是 `device` 包的最小集成参考——把合成帧循环换成你的编码器输出、
接到 `device.FrameHub` 就是生产接线。

## platform-uas —— 平台端（UAS）最小服务器

设备注册进来的那半边：SIP 监听、digest 鉴权、保活存活窗、目录处理、
INVITE 起流进 FrameHub 扇出、告警经 `EventBus` 订阅打印。设备/通道
持久化是 `DeviceStore` 接缝——示例给的是内存实现，真实宿主在这里
接数据库。

```bash
gb28181 platform-uas -listen :5060 -password <设备要呈递的摘要口令>
# 可选：-server-id 34020000002000000001 -realm 3402000000
#       -media-ports 30000-30050 -keepalive-window 60
```

典型用途：让 `device-register`、级联客户端或你的设备实现有一个
"对端"可打；验证设备侧行为时不用真实平台。

## psmux-rtp —— 媒体路径自检

完全自包含的回环：把合成 H.264 access unit 复用成 MPEG-PS、
RTP 打包（PT 96）发给进程内的 UDP 接收端，默认每 50 包打印
包数/字节数统计（`-report-every` 可调）。不监听端口、不依赖对端——
一条命令证明 `psmux` 复用与 RTP 打包路径在当前平台工作正常（设备
角色的直播推流、级联角色的流转发用的都是这条路径）。

典型用途：新架构/新平台上的二进制冒烟；媒体面问题的最小复现载体。

## 共同说明

- `-password` 占位语义：两个模拟子命令都**必填**口令（没有默认值，
  避免误用占位口令连不上还以为库有 bug）。
- 子命令文件本身就是集成样例：读 `cmd/gb28181/*.go` 与读示例无异。
- 工具随 release 演进，能力面跟随库版本——各版本差异见仓库
  [CHANGELOG](https://github.com/mickeyzzc/gb28181-go/blob/main/CHANGELOG.md)。
