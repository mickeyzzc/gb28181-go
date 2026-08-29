package nalutil

// H.265 parameter-set coverage: VPS/SPS/PPS extraction, completeness
// gating, and CRA (21) keyframe detection — encoders like x265 emit CRA
// for every keyframe after the first, so missing it breaks segment
// rollover downstream.

import (
	"bytes"
	"testing"
)

// h265NALU builds a one-byte-header NALU of the given H.265 type.
func h265NALU(t byte, payload ...byte) []byte {
	header := byte(t) << 1
	return append([]byte{header}, payload...)
}

func TestExtractParamSetsH265(t *testing.T) {
	vps := h265NALU(32, 0xAA)
	sps := h265NALU(33, 0xBB)
	pps := h265NALU(34, 0xCC)

	gotVPS, gotSPS, gotPPS := ExtractParamSetsH265([][]byte{vps, sps, pps, h265NALU(19)})
	if !bytes.Equal(gotVPS, vps) || !bytes.Equal(gotSPS, sps) || !bytes.Equal(gotPPS, pps) {
		t.Errorf("extracted (%x %x %x), want (%x %x %x)", gotVPS, gotSPS, gotPPS, vps, sps, pps)
	}

	// Later parameter sets of the same type win (post-encoder-change AU).
	vps2 := h265NALU(32, 0xEE)
	gotVPS, _, _ = ExtractParamSetsH265([][]byte{vps, vps2})
	if !bytes.Equal(gotVPS, vps2) {
		t.Errorf("last VPS should win: %x", gotVPS)
	}

	// Missing sets come back nil; empty NALUs are skipped.
	gotVPS, gotSPS, gotPPS = ExtractParamSetsH265([][]byte{nil, {}, h265NALU(33)})
	if gotVPS != nil || gotSPS == nil || gotPPS != nil {
		t.Errorf("partial AU = (%x %x %x), want (nil sps nil)", gotVPS, gotSPS, gotPPS)
	}
}

func TestHasCompleteParamSets(t *testing.T) {
	h264AU := [][]byte{{0x67, 0xAA}, {0x68, 0xBB}, {0x65, 0xCC}} // SPS PPS IDR
	if !HasCompleteParamSets(h264AU, false) {
		t.Error("H.264 AU with SPS+PPS should be complete")
	}

	if HasCompleteParamSets([][]byte{{0x67, 0xAA}}, false) {
		t.Error("H.264 AU without PPS should be incomplete")
	}

	h265Full := [][]byte{h265NALU(32), h265NALU(33), h265NALU(34)}
	if !HasCompleteParamSets(h265Full, true) {
		t.Error("H.265 AU with VPS+SPS+PPS should be complete")
	}

	if HasCompleteParamSets([][]byte{h265NALU(33), h265NALU(34)}, true) {
		t.Error("H.265 AU without VPS should be incomplete")
	}

	if HasCompleteParamSets(nil, false) || HasCompleteParamSets(nil, true) {
		t.Error("empty AU can never be complete")
	}
}

func TestIsKeyframeNALUH265(t *testing.T) {
	// Random-access types 16-23 (BLA/IDR/CRA) are keyframes.
	for ty := byte(16); ty <= 23; ty++ {
		if !IsKeyframeNALU(h265NALU(ty), true) {
			t.Errorf("H.265 NAL type %d should be a keyframe", ty)
		}
	}

	// CRA (21) is the critical one: x265 emits it for every keyframe
	// after the first.
	for _, ty := range []byte{0, 1, 6, 32, 33, 34, 39, 40} {
		if IsKeyframeNALU(h265NALU(ty), true) {
			t.Errorf("H.265 NAL type %d should not be a keyframe", ty)
		}
	}

	if IsKeyframeNALU(nil, true) {
		t.Error("empty NALU is not a keyframe")
	}

	if !IsIDR([][]byte{h265NALU(32), h265NALU(21)}, true) {
		t.Error("AU with CRA should count as IDR-bearing")
	}
}
