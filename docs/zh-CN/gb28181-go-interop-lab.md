# 无硬件联调国标全链路（interop lab）

手头没有真摄像头、也没有可用的 GB/T 28181 平台，但需要验证协议栈、
给平台联调一个对端、或者只是想亲眼看到"注册 → 保活 → 目录 → 拉流 →
挂断"全链路跑起来——这就是本教程要做的事。全部工具随
[release](https://github.com/mickeyzzc/gb28181-go/releases) 预编译
（七个平台、`CGO_ENABLED=0` 纯静态、零依赖），也可以直接跑源码。
各子命令的完整旗标参考见
[Release 工具包手册](gb28181-go-tools.md)。

## 0. 安装（三选一）

```bash
# 1) 预编译包（推荐：拷到任意 Linux/Windows/macOS 机器即用）
gh release download v0.14.0 -R mickeyzzc/gb28181-go \
  -p 'gb28181-cli_linux_amd64.tar.gz' && tar xzf gb28181-cli_linux_amd64.tar.gz

# 2) 按版本锚定安装
go install github.com/mickeyzzc/gb28181-go/cmd/gb28181@v0.14.0

# 3) 检出仓库直接跑
go run ./cmd/gb28181 <子命令>
```

默认参数是互相咬合的（设备 ID/通道 ID/域/服务器 ID/认证域已成对预设），
下面的命令不改旗标即可对通。

## 1. 终端一：起一台最小平台

```bash
gb28181 platform-uas -listen :5060 -password secret
```

它是一个真 UAS：SIP 监听、digest 鉴权、保活存活窗、目录处理、告警
EventBus 订阅打印。启动后静默等待设备上线。

## 2. 终端二：把虚拟相机注册进去

```bash
gb28181 device-register -platform 127.0.0.1:5060 -password secret
```

它会一直运行（注册刷新 + 保活 + 应答查询），Ctrl-C 结束。本机 5060
已被其他 SIP 服务占用时（报 `bind: address already in use`），加
`-local-sip-port 5061` 换个本地端口即可——虚拟相机是主叫方，本地端口
不影响对通。

终端一/二里应能看到同一套节奏：

1. **REGISTER → 401（digest 挑战）→ 带 Authorization 的 REGISTER → 200**；
2. **保活**：设备每 60s 发 Keepalive MESSAGE，平台答 200（默认存活窗
   60s，`-keepalive-window` 可调）；
3. **目录**：平台的目录查询得到设备自身通道的 Catalog 应答。

到这一步，信令腿（注册/鉴权/保活/目录）已在两台进程间活体运行——
这就是平台开发者的"虚拟摄像头"：把 `-platform` 指到任何真实平台的
SIP 地址即可给它做联调与回归（同样适用于 `-transport tcp` 与
`tls`/SIPS 模式）。

## 3. 媒体腿 + 全链路自证（一条命令）

CLI 的两个模拟子命令是纯信令 + 源就绪（`device-register` 在 INVITE 到
来后即推合成流，1 IDR + 1 P 帧/秒；`platform-uas` 把 INVITE 的宿主接缝
`InviteChannel`/`ByeChannel` 以注释形式展示在
[main.go](../../cmd/gb28181/platform_uas.go) 里）。要把**含媒体在内的
全链路**一次跑通，仓库自带了 conformance 回环套件——真 `device.Server`
对真 `platform/sip.Server`，每个测试就是一段全链路剧本：

```bash
go test ./conformance/ -v
```

- `TestLoopback_RegisterCatalogKeepalive` — 注册+digest、目录、保活存活窗；
- `TestLoopback_InviteRoundTripBYE` — INVITE 起流 → **字节级 RTP/PS
  回环**（设备 psmux 推流 → 平台重组 → psdemux）→ BYE 挂断；
- `TestLoopback_SIPSRegisterCatalog` — TLS 信令（SIPS）变体；
- `TestLoopback_VoiceBroadcast` — 语音广播全流程；
- `TestSoakInviteCycles` — INVITE 循环浸泡（会话建立/拆除的稳定性）。

这套件的意义（也是它比单侧假 socket 测试值钱的地方）：**UAC 与 UAS
必须就同一份协议读法达成一致**——字段编码、branch/CSeq 纪律、SDP 形状
——且每次 CI 都重跑。所谓"开发者自证"，指的就是把这个命令的输出贴进
你的集成记录。

## 4. 媒体路径单独冒烟

```bash
gb28181 psmux-rtp
```

不监听端口、不依赖对端：合成 H.264 → MPEG-PS → RTP 打包 → 进程内 UDP
接收 → 每 50 包打印统计。新架构/新平台上的二进制冒烟、媒体面问题的
最小复现载体。

## 5. 常用变体速查

| 场景 | 命令变化 |
|---|---|
| 平台在另一台机器 | `-platform 192.168.1.10:5060` |
| TCP 信令 | 设备端 `-transport tcp`；平台侧需监听 TCP——CLI 未暴露传输旗标，库接缝是 `platform/sip.Config.SIPTransport` |
| SIPS（TLS，GB/T 28181-2022） | `device-register -transport tls`，对端用支持 SIPS 的平台 |
| 换设备身份 | `-device-id` / `-channel-id` / `-domain` / `-name` |
| 对接旧国密/等保平台 | 不适用——GB35114 走库接缝，见 [GB35114 手册](gb28181-go-gb35114.md) |
| 长稳测试 | 调 `-register-interval` / `-heartbeat-interval`，配 `TestSoakInviteCycles` |

## 6. 排障表

| 症状 | 原因与处置 |
|---|---|
| REGISTER 反复 401 | 两端 `-password` 不一致；digest 口令必须与平台预配的完全相同 |
| 设备注册成功但平台看不到目录 | 平台未发 Catalog 查询或未处理分页应答（大目录分多 MESSAGE，按 SN 累积） |
| 设备端日志无任何应答 | 平台地址/端口不通（`-platform` host:port 写反、防火墙拦 UDP 5060） |
| INVITE 后无流 | 平台 SDP 必须带 `y=`（SSRC）；媒体端口段 `-media-ports` 是否被防火墙拦 |
| TLS/SIPS 握手失败 | 平台侧证书信任链；`tls` 模式对端必须是 SIPS 能力平台 |
| `psmux-rtp` 统计为 0 | 二进制与平台架构不匹配（换对应 `gb28181-cli_<os>_<arch>` 包） |

## 7. 下一步

- 把虚拟相机的合成帧换成真实编码器输出：读
  [device-register 源码](../../cmd/gb28181/device_register.go)（它同时
  是 `device` 包的最小集成样例，帧源换接 `device.FrameHub` 即生产接线）；
- 平台侧持久化/事件接缝：`DeviceStore` 接缝与 `EventBus` 主题见
  [平台端手册](gb28181-go-platform.md)；
- 国密场景：[GB35114 手册](gb28181-go-gb35114.md)。
