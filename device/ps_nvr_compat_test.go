package device_test

import (
	"bytes"
	"testing"
	"time"

	"github.com/mickeyzzc/gb28181-go/device"
	"github.com/mickeyzzc/gb28181-go/platform"
)

// TestMuxH264ToPSFeedsNVRDemuxer pins the acceptance gate of gb28181-go #167
// / gb28181-rs #111: the device muxer's (now standard-conformant) output must
// demux cleanly through the NVR receive path — platform.PSDemuxer — the way
// MiBeeNvr consumes device streams. The NVR demuxer is dual-layout tolerant
// (legacy firmware quirk + standard §2.4.3.6), so this contract must hold for
// the corrected bytes just as it held for the legacy ones.
func TestMuxH264ToPSFeedsNVRDemuxer(t *testing.T) {
	sps := []byte{0x67, 0x42, 0x80, 0x28, 0xDA, 0x01, 0xE0, 0x08}
	pps := []byte{0x68, 0xCE, 0x3C, 0x80}
	idr := []byte{0x65, 0x88, 0x84, 0x00, 0x4B, 0x00, 0x01, 0x00}

	ps := device.MuxH264ToPS([][]byte{sps, pps, idr}, true, time.Unix(1, 0), time.Unix(1, 0))

	demuxer := platform.NewPSDemuxer()
	nalus, err := demuxer.FeedAU(ps, 90000, true)
	if err != nil {
		t.Fatalf("NVR demuxer rejected the muxed keyframe: %v", err)
	}
	want := [][]byte{sps, pps, idr}
	if len(nalus) != len(want) {
		t.Fatalf("got %d NALUs, want %d: %x", len(nalus), len(want), nalus)
	}
	for i := range want {
		if !bytes.Equal(nalus[i], want[i]) {
			t.Errorf("NALU %d mismatch:\n got: %x\nwant: %x", i, nalus[i], want[i])
		}
	}
}

// The >64KB split path feeds the same demuxer: a 200KB access unit muxes into
// four bounded PES packets and the NVR demuxer must reassemble the full ES
// (continuation PES carry no timestamps — the strict length walk plus the
// demuxer's dual-layout calibration recover every chunk).
func TestMuxH264ToPSLargeAUFeedsNVRDemuxer(t *testing.T) {
	// Deterministic filler with no zero bytes: cannot fake Annex-B or PS
	// start codes inside the elementary stream.
	idr := make([]byte, 200_000)
	x := uint32(0x12345678)
	for i := range idr {
		x = x*1664525 + 1013904223
		idr[i] = byte(1 + (x>>16)%255)
	}
	idr[0] = 0x65

	ps := device.MuxH264ToPS([][]byte{idr}, true, time.Unix(1, 0), time.Unix(1, 0))

	demuxer := platform.NewPSDemuxer()
	nalus, err := demuxer.FeedAU(ps, 90000, true)
	if err != nil {
		t.Fatalf("NVR demuxer rejected the split large AU: %v", err)
	}
	if len(nalus) != 1 {
		t.Fatalf("got %d NALUs, want 1 reassembled access unit", len(nalus))
	}
	if !bytes.Equal(nalus[0], idr) {
		t.Errorf("reassembled NALU mismatch: got %d bytes, want %d", len(nalus[0]), len(idr))
	}
}
