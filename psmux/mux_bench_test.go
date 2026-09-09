package psmux

// Throughput benchmarks (issue #45): the per-AU cost of PS muxing — the
// hot path every frame of every forwarding session pays.

import (
	"testing"
)

func benchNALU() []byte {
	// ~120 KB IDR-shaped AU payload (keyframe at 4K bitrate).
	nalu := make([]byte, 120*1024)
	nalu[0] = 0x65
	nalu[4] = 0x01
	nalu[5] = 0x02
	nalu[3], nalu[4] = 0x00, 0x01 // Annex-B start code
	return append([]byte{0x00, 0x00, 0x00, 0x01}, nalu...)
}

func BenchmarkPsmuxWriteAU_Keyframe(b *testing.B) {
	au := benchNALU()
	m := New()
	m.SetVideoCodec("h264")
	b.SetBytes(int64(len(au)))
	b.ResetTimer()
	for range b.N {
		_ = m.WriteAU(au, int64(b.N), true)
	}
}

func BenchmarkPsmuxWriteAU_Pframe(b *testing.B) {
	p := make([]byte, 1200)
	p[0] = 0x41
	au := append([]byte{0x00, 0x00, 0x00, 0x01}, p...)
	m := New()
	m.SetVideoCodec("h264")
	b.SetBytes(int64(len(au)))
	b.ResetTimer()
	for range b.N {
		_ = m.WriteAU(au, int64(b.N), false)
	}
}
