package platform

// ES-over-RTP receive side (issue #110, RFC 6184 / RFC 7798): the
// depacketizer turns RTP payloads carrying bare H.264/H.265 NAL units
// (single NALU / STAP-A / FU-A, H.265 single / AP / FU) into the same
// [][]byte AU shape the PS path produces, so FrameHub consumers do not
// change.

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func nalu(t byte, size int) []byte {
	b := make([]byte, size)
	b[0] = t
	for i := 1; i < size; i++ {
		b[i] = byte(i)
	}
	return b
}

func TestESDemuxerSingleNALUs(t *testing.T) {
	d := NewESDemuxer()
	sps := nalu(7, 8)
	idr := nalu(5, 64)
	require.NoError(t, d.FeedPacket(sps))
	require.NoError(t, d.FeedPacket(idr))
	au := d.TakeAU()
	require.Len(t, au, 2, "both NALUs collected")
	require.Equal(t, sps, au[0])
	require.Equal(t, idr, au[1])
	require.Empty(t, d.TakeAU(), "TakeAU drains")
	require.Equal(t, "h264", d.Codec())
}

func TestESDemuxerSTAPA(t *testing.T) {
	d := NewESDemuxer()
	a := nalu(7, 6)
	b := nalu(5, 40)
	payload := []byte{24} // STAP-A NAL header (F=0, NRI=0, type=24)
	payload = append(payload, byte(len(a)>>8), byte(len(a)))
	payload = append(payload, a...)
	payload = append(payload, byte(len(b)>>8), byte(len(b)))
	payload = append(payload, b...)
	require.NoError(t, d.FeedPacket(payload))
	au := d.TakeAU()
	require.Len(t, au, 2)
	require.Equal(t, a, au[0])
	require.Equal(t, b, au[1])
}

func TestESDemuxerFUAAcrossPackets(t *testing.T) {
	d := NewESDemuxer()
	big := nalu(5, 120) // reconstructed header comes from FU bits
	fuHdr := byte(5)    // original type
	// Packet 1: FU-A start
	p1 := []byte{28, 1<<7 | fuHdr} // FU indicator (type 28) + FU header S=1
	p1 = append(p1, big[1:40]...)
	// Packet 2: continuation
	p2 := []byte{28, fuHdr}
	p2 = append(p2, big[40:90]...)
	// Packet 3: end
	p3 := []byte{28, 1<<6 | fuHdr}
	p3 = append(p3, big[90:]...)

	require.NoError(t, d.FeedPacket(p1))
	require.Empty(t, d.TakeAU(), "AU not complete before the end fragment")
	require.NoError(t, d.FeedPacket(p2))
	require.Empty(t, d.TakeAU())
	require.NoError(t, d.FeedPacket(p3))

	au := d.TakeAU()
	require.Len(t, au, 1)
	require.Equal(t, big, au[0], "FU-A reassembly reconstructs the NALU byte-exactly")
}

// A lost FU start drops the fragments until the next start.
func TestESDemuxerFULostStart(t *testing.T) {
	d := NewESDemuxer()
	cont := []byte{28, 5, 0xAA, 0xBB}
	require.Error(t, d.FeedPacket(cont), "continuation without a start is an error")
	require.Empty(t, d.TakeAU())

	// Recovery on the next start.
	big := nalu(1, 30)
	start := append([]byte{28, 1<<7 | 1}, big[1:15]...)
	end := append([]byte{28, 1<<6 | 1}, big[15:]...)
	require.NoError(t, d.FeedPacket(start))
	require.NoError(t, d.FeedPacket(end))
	au := d.TakeAU()
	require.Len(t, au, 1)
	require.Equal(t, big, au[0])
}

func TestESDemuxerDropPartial(t *testing.T) {
	d := NewESDemuxer()
	big := nalu(5, 50)
	start := append([]byte{28, 1<<7 | 5}, big[1:25]...)
	require.NoError(t, d.FeedPacket(start))
	require.NoError(t, d.FeedPacket(nalu(7, 8)))
	d.DropPartial()
	require.Empty(t, d.TakeAU(), "pending FU and collected NALUs are dropped")

	// Clean state after the drop.
	idr := nalu(5, 20)
	require.NoError(t, d.FeedPacket(idr))
	require.Equal(t, [][]byte{idr}, d.TakeAU())
}

// H.265 (RFC 7798): VPS leads with a 2-byte header; type 32 maps onto
// invalid H.264 types, so the first packet latches the codec.
func TestESDemuxerH265SingleAndFU(t *testing.T) {
	d := NewESDemuxer()
	vps := make([]byte, 10)
	vps[0] = byte(32 << 1) // F=0, type=32 (VPS), layer high bit 0
	vps[1] = 0x01          // layer id low + tid
	for i := 2; i < len(vps); i++ {
		vps[i] = byte(i)
	}
	require.NoError(t, d.FeedPacket(vps))
	require.Equal(t, "h265", d.Codec(), "VPS-only-in-H.265 latches the codec")
	require.Equal(t, [][]byte{vps}, d.TakeAU())

	// H.265 FU (type 49): 2-byte NAL header + FU header (S|E|type 6 bits).
	big := make([]byte, 60)
	big[0] = byte(19 << 1) // IDR_W_RADL
	big[1] = 0x01
	for i := 2; i < len(big); i++ {
		big[i] = byte(0x50 + i)
	}
	ind := []byte{byte(49 << 1), 0x01}
	p1 := append(append([]byte{}, ind...), 1<<7|19)
	p1 = append(p1, big[2:20]...)
	p2 := append(append([]byte{}, ind...), 19)
	p2 = append(p2, big[20:40]...)
	p3 := append(append([]byte{}, ind...), 1<<6|19)
	p3 = append(p3, big[40:]...)
	require.NoError(t, d.FeedPacket(p1))
	require.NoError(t, d.FeedPacket(p2))
	require.NoError(t, d.FeedPacket(p3))
	au := d.TakeAU()
	require.Len(t, au, 1)
	require.Equal(t, big, au[0], "H.265 FU reassembly reconstructs the 2-byte header")
}

// STAP-A with a truncated entry is rejected, not silently misparsed.
func TestESDemuxerSTAPAMalformed(t *testing.T) {
	d := NewESDemuxer()
	bad := []byte{24, 0x00, 0x20, 0xAA, 0xBB} // claims 32 bytes, has 2
	require.Error(t, d.FeedPacket(bad))
	require.Empty(t, d.TakeAU())
}

// Receiver-level: ES RTP packets flow to FrameHub with the same AU shape
// as the PS path (issue #110 requirement 3), and the session stays ES
// once latched.
func TestReceiverESOverRTPBasic(t *testing.T) {
	hub := NewFrameHub()
	pm := NewPortManager(21000, 21010)
	rec := NewReceiver("es-cam", hub, pm)

	serverConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	require.NoError(t, err)
	defer serverConn.Close()
	clientConn, err := net.DialUDP("udp", nil, serverConn.LocalAddr().(*net.UDPAddr))
	require.NoError(t, err)
	defer clientConn.Close()

	var mu sync.Mutex
	var frames [][][]byte
	var idrFlags []bool
	require.NoError(t, hub.Subscribe("es-consumer", func(pts int64, au [][]byte, isIDR bool) {
		mu.Lock()
		defer mu.Unlock()
		frames = append(frames, au)
		idrFlags = append(idrFlags, isIDR)
	}))
	defer hub.Unsubscribe("es-consumer")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, rec.Start(ctx, serverConn))
	defer rec.Stop()

	// AU 1: SPS (single NALU) + IDR split over FU-A, marker on the end
	// fragment.
	sps := nalu(7, 16)
	_, err = clientConn.Write(buildTestRTPPacket(0, 90000, sps, false))
	require.NoError(t, err)

	idr := nalu(5, 100)
	f1 := append([]byte{28, 1<<7 | 5}, idr[1:50]...)
	f2 := append([]byte{28, 1<<6 | 5}, idr[50:]...)
	_, err = clientConn.Write(buildTestRTPPacket(1, 90000, f1, false))
	require.NoError(t, err)
	_, err = clientConn.Write(buildTestRTPPacket(2, 90000, f2, true))
	require.NoError(t, err)

	// AU 2: one non-IDR single NALU, marker set.
	p := nalu(1, 30)
	_, err = clientConn.Write(buildTestRTPPacket(3, 90360, p, true))
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(frames) >= 2
	}, 2*time.Second, 10*time.Millisecond, "consumer should receive both AUs")

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, frames[0], 2, "AU1 carries SPS + IDR")
	require.Equal(t, sps, frames[0][0])
	require.Equal(t, idr, frames[0][1])
	require.Len(t, frames[1], 1, "AU2 carries the P-frame NALU")
	require.True(t, idrFlags[0])
	require.False(t, idrFlags[1])
	require.Equal(t, "h264", rec.Codec())
}

// A PS stream after an ES-latched session stays ES (mode is per
// Receiver, streams never mix) — asserted indirectly: the mode latch
// survives; here we only pin that a fresh Receiver classifies PS
// correctly so ES never shadows the PS path.
func TestReceiverPSNotMisclassified(t *testing.T) {
	require.False(t, looksLikeESPacket([]byte{0x00, 0x00, 0x01, 0xBA, 0x00}))
	require.True(t, looksLikeESPacket([]byte{0x67, 0x42}))
	require.True(t, looksLikeESPacket([]byte{0x1C, 0x85}))
	require.True(t, looksLikeESPacket([]byte{0x40, 0x01}))  // h265 VPS
	require.False(t, looksLikeESPacket([]byte{0x81, 0x00})) // forbidden bit
}
