# PS 封装与 RTP 打包

`psmux` 包把 H.264/H.265（+ G.711 音频）封进 MPEG-2 节目流并打包
上 RTP——设备推流路径与平台/级联转发共用。

## 封装器

```go
import "github.com/mickeyzzc/gb28181-go/psmux"

m := psmux.New()
m.SetVideoCodec("h264") // 或 "h265"
m.SetAudioCodec("pcma") // 可选 G.711

// WriteAU 吃完整 Annex-B 访问单元（带起始码）。
ps := m.WriteAU(annexBAU, pts, isIDR)
audioPS := m.WriteAudio(g711Frame, pts)
```

- IDR 访问单元在 PES 前发 pack header + system header + PSM；
  P 帧只发 pack header + PES。
- 超出 PES 长度上限的访问单元自动切成多个有界 PES 包。

## RTP 打包

```go
// UDP 推流:
p := psmux.NewRTPPacketizer(conn, udpAddr, ssrc, initialSeq)
err := p.Send(ps, tsTicks)

// TCP 被动媒体（国标 TCP 模式）:
p := psmux.NewRTPPacketizerTCP(conn, ssrc, initialSeq)
```

`Send` 把超长 PS 自动分片为多个标记位正确的 RTP 包；`Sent()` 报
已发包数。

## 该用哪个封装器？

两个封装器**有意共存**：

- `device.MuxH264ToPS`——仅 H.264，真机平台实战检验过，与 Rust
  孪生库逐字节金串对齐。需要孪生一致字节时用它。
- `psmux.New()`——支持 H.265 + G.711 音频，平台转发与级联在用。
  新集成需要 H.265/音频时用它。

合并推迟的原因见 README 的"API 面说明"。

## nalutil

`nalutil` 回答两个角色都要问 NAL 流的问题：这个访问单元是不是 IDR、
提取 SPS/PPS（/VPS）、比较跨重启的参数集（编码器重启检测）。
