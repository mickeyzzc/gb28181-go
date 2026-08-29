package cascade

import (
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"

	"github.com/mickeyzzc/gb28181-go/platform"
	"github.com/mickeyzzc/gb28181-go/psmux"
	"github.com/stretchr/testify/require"
)

// The FIRST burst of a forwarding session must carry the PSM even when the
// hub starts delivering mid-GOP (P-frames only): receivers latch demuxer
// codec and IDR tracking from the PSM, and an IDR-less start would hide it
// for up to a full GOP — observed live as H.265 channels mis-detected on the
// upper platform (MiBeeNvr issue #625: FLV/WS 503 + 17s keyframe recycling).
func TestMediaSessionFirstBurstCarriesPSM(t *testing.T) {
	mainHub := platform.NewFrameHub()
	svc := New(testCfg(), hubSource{fakeSource{cams: []CameraInfo{{ID: "cam-1"}}}, mainHub}, nil)

	client, srvConn := net.Pipe()
	ms := &mediaSession{
		svc: svc, callID: "c1", channel: "ch", camera: "cam-1",
		mux: psmux.New(), codecHint: "h265",
		rtp: psmux.NewRTPPacketizerTCP(srvConn, 1, 0),
	}
	ms.mux.SetVideoCodec("h265")
	ms.hub = mainHub
	ms.run(mainHub)
	defer ms.stop()

	// Mid-GOP join: P-frame only AU (H.265 TRAIL_R slice — no param sets,
	// auIsIDR must be false for it).
	au := [][]byte{{0x02, 0x01, 0x02, 0x03, 0x04}}
	mainHub.Broadcast(90000, au, false)

	ps := readFirstBurstPS(t, client)
	require.Contains(t, string(ps), "\x00\x00\x01\xbc",
		"first burst must carry the PSM (00 00 01 BC) even without an IDR")
	require.Contains(t, string(ps), "\x00\x00\x01\xbb",
		"first burst must carry the system header (00 00 01 BB)")

	// The SECOND burst (still P-frames) must NOT re-send the PSM.
	mainHub.Broadcast(93600, au, false)
	ps2 := readFirstBurstPS(t, client)
	require.NotContains(t, string(ps2), "\x00\x00\x01\xbc",
		"only the first burst is PSM-forced; later bursts follow auIsIDR")
}

// readFirstBurstPS drains RTP packets from the pipe until one carries the
// marker bit (end of burst) and returns the concatenated payloads.
func readFirstBurstPS(t *testing.T, c net.Conn) []byte {
	t.Helper()
	c.SetReadDeadline(time.Now().Add(3 * time.Second))
	var ps []byte
	for i := 0; i < 8; i++ {
		hdr := make([]byte, 2)
		if _, err := io.ReadFull(c, hdr); err != nil {
			t.Fatalf("read framing header: %v", err)
		}
		n := binary.BigEndian.Uint16(hdr)
		pkt := make([]byte, n)
		if _, err := io.ReadFull(c, pkt); err != nil {
			t.Fatalf("read rtp packet: %v", err)
		}
		require.GreaterOrEqual(t, len(pkt), 12)
		ps = append(ps, pkt[12:]...)
		if pkt[1]&0x80 != 0 {
			return ps
		}
	}
	t.Fatal("no marker bit within 8 packets")
	return nil
}
