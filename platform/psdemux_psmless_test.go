package platform

import (
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/require"
)

// Minimal PS framing for PSM-less bursts — byte-identical layouts to the
// psmux package's packHeader/videoPES (its round-trip tests pin the formats).
func packHeaderForTest(pts int64) []byte {
	scr := pts / 300
	if scr < 0 {
		scr = 0
	}
	h := []byte{0x00, 0x00, 0x01, 0xBA}
	h = append(h, byte(scr>>27)&0x18|0x44)
	h = append(h, byte(scr>>19), byte(scr>>11)|0x01, byte(scr>>3), byte(scr&0x7)<<5|0x04)
	h = append(h, 0x01)
	rate := uint32(70000)
	h = append(h, byte(rate>>14), byte(rate>>6), byte(rate&0x3F)<<2|0x03)
	h = append(h, 0xF8)
	return h
}

func videoPESForTest(payload []byte, pts int64) []byte {
	p := uint64(pts) & 0x1FFFFFFFF
	ptsB := []byte{
		0x21 | byte((p>>30)&0x07)<<1 | 1,
		byte(p >> 22),
		byte(p>>14)&0xFE | 1,
		byte(p >> 7),
		byte(p<<1)&0xFE | 1,
	}
	pes := []byte{0x00, 0x00, 0x01, 0xE0}
	pes = binary.BigEndian.AppendUint16(pes, uint16(8+len(payload)))
	pes = append(pes, 0x80, 0x80, 0x05)
	pes = append(pes, ptsB...)
	return append(pes, payload...)
}

// A PSM-less stream (compliant sender whose PSM never reaches a mid-stream
// joiner, or a non-compliant device) must still latch the codec from
// definitive parameter-set NALUs — without it the demuxer reports "" and AU
// splitting + downstream IDR detection run in generic mode (MiBeeNvr #625:
// H.265 cascade channels mis-detected → FLV/WS 503 + keyframe-watchdog
// recycling on the upper platform).
func TestPSDemuxerLatchesCodecFromParamSetsWithoutPSM(t *testing.T) {
	d := NewPSDemuxer()

	// Hand-built PS burst WITHOUT a PSM: pack header + video PES carrying an
	// H.265 param-set AU (VPS 0x40, SPS 0x42, PPS 0x44).
	es := []byte{0, 0, 0, 1, 0x40, 0x01, 0x0c, 0x01,
		0, 0, 0, 1, 0x42, 0x01, 0x01,
		0, 0, 0, 1, 0x44, 0x01,
		0, 0, 0, 1, 0x26, 0x01, 0xaf, 0x06}
	ps := append(packHeaderForTest(90000), videoPESForTest(es, 90000)...)

	nalus, err := d.FeedAU(ps, 9000, true)
	require.NoError(t, err)
	require.NotEmpty(t, nalus)
	require.Equal(t, "h265", d.Codec(), "param-set NALUs must latch the codec without a PSM")

	// Once latched (or PSM-declared), content cannot flip it back.
	ps264 := append(packHeaderForTest(93600), videoPESForTest(
		[]byte{0, 0, 0, 1, 0x67, 0x42, 0x00, 0x1e}, 93600)...)
	_, err = d.FeedAU(ps264, 9360, true)
	require.NoError(t, err)
	require.Equal(t, "h265", d.Codec(), "a latched codec must be sticky")
}

// The mirror case: H.264 SPS latches h264 on a PSM-less stream.
func TestPSDemuxerLatchesH264WithoutPSM(t *testing.T) {
	d := NewPSDemuxer()
	es := []byte{0, 0, 0, 1, 0x67, 0x42, 0x00, 0x1e,
		0, 0, 0, 1, 0x68, 0xce, 0x38, 0x80,
		0, 0, 0, 1, 0x65, 0x88, 0x84}
	ps := append(packHeaderForTest(90000), videoPESForTest(es, 90000)...)
	_, err := d.FeedAU(ps, 9000, true)
	require.NoError(t, err)
	require.Equal(t, "h264", d.Codec())
}

// P-frames only (no param sets anywhere) must NOT latch anything — the empty
// codec stays available for a later PSM or param-set AU.
func TestPSDemuxerNoLatchFromSlicesOnly(t *testing.T) {
	d := NewPSDemuxer()
	es := []byte{0, 0, 0, 1, 0x02, 0x01, 0x02, 0x03}
	ps := append(packHeaderForTest(90000), videoPESForTest(es, 90000)...)
	_, err := d.FeedAU(ps, 9000, true)
	require.NoError(t, err)
	require.Equal(t, "", d.Codec(), "slice-only AU must not latch a codec guess")
}
