// Media-path example: mux H.264 access units into MPEG-PS and packetize
// as RTP to a receiver (the same path the device role uses for live push
// and the cascade role uses for stream forwarding). A UDP listener
// imitates the receiver and prints packet stats.
package main

import (
	"fmt"
	"net"
	"time"

	"github.com/mickeyzzc/gb28181-go/psmux"
)

// annexB joins NALUs with Annex-B start codes — the muxer input format.
func annexB(nalus [][]byte) []byte {
	var out []byte
	for _, nalu := range nalus {
		out = append(out, 0, 0, 0, 1)
		out = append(out, nalu...)
	}
	return out
}

func main() {
	// Receiver side: one UDP socket, counts packets/bytes.
	recv, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		panic(err)
	}
	defer recv.Close()
	dst := recv.LocalAddr().(*net.UDPAddr)

	go func() {
		buf := make([]byte, 65535)
		var packets, bytes int
		for {
			n, _, err := recv.ReadFromUDP(buf)
			if err != nil {
				return
			}
			packets++
			bytes += n
			if packets%50 == 0 {
				fmt.Printf("receiver: %d packets, %d bytes\n", packets, bytes)
			}
		}
	}()

	// Sender side: PS mux + RTP packetizer (payload type 96, SSRC
	// 0100000001 = live-stream SSRC per GB/T 28181).
	mux := psmux.New()
	mux.SetVideoCodec("h264")                              // or "h265" / "svac"
	rtp := psmux.NewRTPPacketizer(nil, dst, 1000000001, 1) // SSRC, initial seq

	// Access units as Annex-B streams (start-code framed NALUs) — the
	// muxer's input format. One tiny IDR per five seconds, P frames between.
	idr := annexB([][]byte{{0x67, 0x64, 0x00, 0x1F}, {0x68, 0xEB, 0xE3, 0xCB}, {0x65, 0x01, 0x02, 0x03}})
	p := annexB([][]byte{{0x41, 0x02}})

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for i := 0; ; i++ {
		<-ticker.C
		au := p
		if i%5 == 0 {
			au = idr
		}
		ps := mux.WriteAU(au, int64(90000*(i+1)), i%5 == 0)
		if err := rtp.Send(ps, int64(90000*(i+1))); err != nil {
			fmt.Printf("send: %v\n", err)
			return
		}
	}
}
