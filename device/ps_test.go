package device

import (
	"bytes"
	"encoding/hex"
	"testing"
	"time"
)

// TestPsPackHeaderLayout verifies the pack header has correct start code and length.
func TestPsPackHeaderLayout(t *testing.T) {
	header := BuildPsPackHeader(27000000, 10000)

	// Check start code
	if header[0] != 0x00 || header[1] != 0x00 || header[2] != 0x01 || header[3] != 0xBA {
		t.Errorf("PS pack header start code should be 0x000001BA, got %02X%02X%02X%02X",
			header[0], header[1], header[2], header[3])
	}

	// Check total length (4 bytes start code + 10 field bytes)
	if len(header) != 14 {
		t.Errorf("PS pack header length should be 14 bytes, got %d", len(header))
	}

	// Check MPEG-2 marker (bits 7-6 = 01, so byte[4] & 0xC0 should be 0x40)
	if header[4]&0xC0 != 0x40 {
		t.Errorf("PS pack header byte[4] bits 7-6 should be 01 (MPEG-2 marker), got %02X", header[4]&0xC0)
	}
}

// TestMuxH264ToPS_EveryAUHasPackHeader verifies pack header is emitted on non-keyframes.
func TestMuxH264ToPS_EveryAUHasPackHeader(t *testing.T) {
	// P-frame NAL unit (non-IDR)
	pFrame := []byte{0x41, 0x88, 0x84, 0x00, 0x4B, 0x00, 0x01, 0x00}
	nalus := [][]byte{pFrame}

	pts := time.Unix(1, 0)
	dts := time.Unix(1, 0)

	// Mux with isKeyFrame=false (P-frame)
	psData := MuxH264ToPS(nalus, false, pts, dts)

	// Output should start with pack header start code
	if len(psData) < 4 {
		t.Fatalf("PS data too short: %d bytes", len(psData))
	}

	if psData[0] != 0x00 || psData[1] != 0x00 || psData[2] != 0x01 || psData[3] != 0xBA {
		t.Errorf("PS data should start with pack header 0x000001BA even on non-keyframe, got %02X%02X%02X%02X",
			psData[0], psData[1], psData[2], psData[3])
	}
}

// TestMuxH264ToPS_PSMOnlyOnKeyframe verifies PSM is only emitted on keyframes.
func TestMuxH264ToPS_PSMOnlyOnKeyframe(t *testing.T) {
	// P-frame NAL unit (non-IDR)
	pFrame := []byte{0x41, 0x88, 0x84, 0x00, 0x4B, 0x00, 0x01, 0x00}
	nalus := [][]byte{pFrame}

	pts := time.Unix(1, 0)
	dts := time.Unix(1, 0)

	// Mux with isKeyFrame=false (P-frame)
	psData := MuxH264ToPS(nalus, false, pts, dts)

	// PSM start code (0x000001BB) should NOT be present
	if bytes.Contains(psData, []byte{0x00, 0x00, 0x01, 0xBB}) {
		t.Error("PSM (0x000001BB) should not be present in non-keyframe output")
	}

	// Now test with keyframe
	idr := []byte{0x65, 0x88, 0x84, 0x00, 0x4B, 0x00, 0x01, 0x00}
	nalusKey := [][]byte{idr}

	psDataKey := MuxH264ToPS(nalusKey, true, pts, dts)

	// PSM start code (0x000001BB) SHOULD be present
	if !bytes.Contains(psDataKey, []byte{0x00, 0x00, 0x01, 0xBB}) {
		t.Error("PSM (0x000001BB) should be present in keyframe output")
	}
}

// TestMuxH264ToPS_Roundtrip verifies NAL units can be recovered from PS.
func TestMuxH264ToPS_Roundtrip(t *testing.T) {
	// Synthetic minimal SPS, PPS, and IDR NAL units
	sps := []byte{0x67, 0x42, 0x80, 0x28, 0xDA, 0x01, 0xE0, 0x08}
	pps := []byte{0x68, 0xCE, 0x3C, 0x80}
	idr := []byte{0x65, 0x88, 0x84, 0x00, 0x4B, 0x00, 0x01, 0x00}

	nalus := [][]byte{sps, pps, idr}

	pts := time.Unix(1, 0) // 1 second after epoch
	dts := time.Unix(1, 0)

	// Mux to PS
	psData := MuxH264ToPS(nalus, true, pts, dts)

	if len(psData) == 0 {
		t.Fatal("PS data should not be empty")
	}

	// Parse back to extract NAL units
	// Find PES packets (0x000001E0 for video)
	pesStart := []byte{0x00, 0x00, 0x01, 0xE0}

	var payloads [][]byte
	offset := 0
	for offset < len(psData) {
		pos := bytes.Index(psData[offset:], pesStart)
		if pos == -1 {
			break
		}
		pos += offset

		// PES packet structure (twin layout, see BuildPesPacket):
		// bytes 0-3: start code + stream ID
		// bytes 4-5: packet length
		// byte 6: flags
		// byte 7: header_data_length
		// bytes 8..: optional header (PTS/DTS)
		// one gap byte, then payload
		if pos+8 > len(psData) {
			break
		}
		// Get header_data_length (byte 7)
		headerDataLen := int(psData[pos+7])
		// Payload starts after 8 + headerDataLen bytes plus the gap byte;
		// locate the first Annex-B start code so leading zeros (gap byte /
		// 4-byte start codes) never offset the extraction.
		payloadPos := pos + 8 + headerDataLen
		if i := bytes.Index(psData[payloadPos:], []byte{0x00, 0x00, 0x01}); i >= 0 {
			payloadPos += i
		}
		if payloadPos < len(psData) {
			payloads = append(payloads, psData[payloadPos:])
			break // Found the first payload
		}
		offset = pos + 1
	}

	if len(payloads) == 0 {
		t.Fatal("No PES payloads found")
	}

	// Extract NAL units from concatenated payload
	combined := payloads[0]
	extractedNalus := extractNALUs(combined)

	if len(extractedNalus) != 3 {
		t.Errorf("Expected 3 NAL units, got %d", len(extractedNalus))
	}

	// Verify SPS, PPS, IDR match byte-for-byte
	if !bytes.Equal(extractedNalus[0], sps) {
		t.Errorf("SPS mismatch: got %v, want %v", extractedNalus[0], sps)
	}
	if !bytes.Equal(extractedNalus[1], pps) {
		t.Errorf("PPS mismatch: got %v, want %v", extractedNalus[1], pps)
	}
	if !bytes.Equal(extractedNalus[2], idr) {
		t.Errorf("IDR mismatch: got %v, want %v", extractedNalus[2], idr)
	}
}

// TestEncodePtsDts verifies PTS/DTS encoding.
func TestEncodePtsDts(t *testing.T) {
	// Test with known value
	value := uint64(90000) // 1 second at 90kHz
	encoded := encodePtsDts(value, 0x2)

	if len(encoded) != 5 {
		t.Errorf("encoded PTS/DTS should be 5 bytes, got %d", len(encoded))
	}

	// Verify first byte has correct prefix (0x2 << 4 = 0x20)
	if encoded[0]&0xF0 != 0x20 {
		t.Errorf("encoded[0] bits 7-4 should be 0x2 (prefix), got %02X", encoded[0]&0xF0)
	}
}

// TestTimeTo90kHz verifies time conversion.
func TestTimeTo90kHz(t *testing.T) {
	// Zero time should give zero ticks
	if timeTo90kHz(time.Time{}) != 0 {
		t.Error("Zero time should give zero ticks")
	}

	// 1 second should give 90000 ticks
	oneSecond := time.Unix(1, 0)
	ticks := timeTo90kHz(oneSecond)
	if ticks != 90000 {
		t.Errorf("1 second should be 90000 ticks, got %d", ticks)
	}

	// 0.1 second should give 9000 ticks
	tenthSecond := time.Unix(0, 100_000_000) // 100ms in nanos
	ticks = timeTo90kHz(tenthSecond)
	if ticks != 9000 {
		t.Errorf("0.1 second should be 9000 ticks, got %d", ticks)
	}
}

// TestBuildPesPacketLengthFieldBalances pins the PES_packet_length contract:
// for a bounded PES the 16-bit field must equal the number of bytes actually
// written after it. Issue #15 regression: the field was computed for a 3-byte
// header while only 2 header bytes (+ gap) were written, leaving every
// timestamped PES one byte longer than its bytes — strict receivers honoring
// the length never completed reassembly and dropped every frame at the AU
// marker ("AU ended mid-PES").
func TestBuildPesPacketLengthFieldBalances(t *testing.T) {
	payload := []byte{0x00, 0x00, 0x00, 0x01, 0x65, 0x88, 0x84}
	cases := []struct {
		name string
		pts  time.Time
		dts  time.Time
	}{
		{"pts+dts", time.Unix(1, 0), time.Unix(1, 0)},
		{"pts only", time.Unix(1, 0), time.Time{}},
		{"no timestamps", time.Time{}, time.Time{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pes := BuildPesPacket(0xE0, payload, tc.pts, tc.dts)
			if len(pes) < 9 {
				t.Fatalf("PES too short: %d bytes", len(pes))
			}
			declared := int(pes[4])<<8 | int(pes[5])
			if declared == 0 {
				t.Fatalf("expected bounded PES, got PES_packet_length=0")
			}
			if got, want := len(pes), 6+declared; got != want {
				t.Errorf("PES_packet_length=%d declares %d bytes after the 6-byte prefix, but %d were written",
					declared, want-6, got-6)
			}
		})
	}
}

// TestMuxH264ToPSMatchesRustTwinGolden pins byte-for-byte wire identity with
// gb28181-rs mux_h264_to_ps (the Rust sister device's library — the reference
// implementation that produces zero demuxer alarms on the NVR platform).
// Goldens generated by gb28181-rs at pts/dts = 90000 / 180000 (90kHz ticks,
// i.e. time.Unix(1,0) / time.Unix(2,0)).
func TestMuxH264ToPSMatchesRustTwinGolden(t *testing.T) {
	sps := []byte{0x67, 0x42, 0x80, 0x28, 0xDA, 0x01, 0xE0, 0x08}
	pps := []byte{0x68, 0xCE, 0x3C, 0x80}
	idr := []byte{0x65, 0x88, 0x84, 0x00, 0x4B, 0x00, 0x01, 0x00}

	// Goldens are the verbatim output of gb28181-rs mux_h264_to_ps
	// (tests/tmp dump, 2026-08-29) — kept as single unsegmented literals.
	keyGolden := "000001ba440016fc8401009c43f8000001bb000b01000000041be000000000000001e0002bc00a310005bf21110005bf21000000000167428028da01e00800000168ce3c80000001658884004b000100"

	psData := MuxH264ToPS([][]byte{sps, pps, idr}, true, time.Unix(1, 0), time.Unix(1, 0))
	if got := hex.EncodeToString(psData); got != keyGolden {
		t.Errorf("keyframe PS diverges from gb28181-rs twin:\n got: %s\nwant: %s", got, keyGolden)
	}

	pGolden := "000001ba44002df90401009c43f8000001e00019c00a31000b7e4111000b7e410000000001658884004b000100"

	psData = MuxH264ToPS([][]byte{idr}, false, time.Unix(2, 0), time.Unix(2, 0))
	if got := hex.EncodeToString(psData); got != pGolden {
		t.Errorf("P-frame PS diverges from gb28181-rs twin:\n got: %s\nwant: %s", got, pGolden)
	}
}

// pesPacket is one walked PES packet of a muxed PS burst.
type pesPacket struct {
	offset    int // position of the 00 00 01 E0 prefix within the burst
	declared  int // PES_packet_length field value
	actual    int // bytes written after the 6-byte prefix
	hasTS     bool
	headerLen int // optional-header byte count from byte 7 (twin layout)
}

// walkPESPackets walks a PS burst the way a strict receiver does: advance by
// each PES's declared length instead of scanning to the end of data.
func walkPESPackets(t *testing.T, psData []byte) []pesPacket {
	t.Helper()
	var out []pesPacket
	pesStart := []byte{0x00, 0x00, 0x01, 0xE0}
	pos := 0
	for pos < len(psData) {
		idx := bytes.Index(psData[pos:], pesStart)
		if idx == -1 {
			break
		}
		p := pos + idx
		if p+9 > len(psData) {
			t.Fatalf("PES at %d truncated before header", p)
		}
		pkt := pesPacket{
			offset:    p,
			declared:  int(psData[p+4])<<8 | int(psData[p+5]),
			headerLen: int(psData[p+7]),
			hasTS:     psData[p+6]&0xC0 != 0,
		}
		if pkt.declared == 0 {
			t.Fatalf("PES at %d is unbounded (length=0) — mux must never emit these", p)
		}
		pkt.actual = len(psData) - p - 6
		if pkt.declared <= pkt.actual {
			pkt.actual = pkt.declared
			out = append(out, pkt)
			pos = p + 6 + pkt.declared
		} else {
			// Strict receiver: pending PES at end of burst — this is the
			// issue #15 failure signature.
			out = append(out, pkt)
			pos = len(psData)
		}
	}
	return out
}

// TestMuxH264ToPSLargeFrameSplitsIntoBoundedPES pins the >64KB path: the 16-bit
// PES_packet_length cannot carry a large access unit, so the mux MUST split the
// elementary stream across continuation PES packets (first carries PTS/DTS,
// continuations carry none). The pre-fix code let the length computation wrap
// mod 65536, silently truncating every large IDR and smearing 64KB of residual
// ES into the next access unit.
func TestMuxH264ToPSLargeFrameSplitsIntoBoundedPES(t *testing.T) {
	// Deterministic filler with no zero bytes: cannot fake Annex-B or PS start
	// codes inside the elementary stream.
	mkNAL := func(n int) []byte {
		b := make([]byte, n)
		x := uint32(0x12345678)
		for i := range b {
			x = x*1664525 + 1013904223
			b[i] = byte(0x01 + (x>>16)%255)
		}
		return b
	}

	idr := append([]byte{0x65}, mkNAL(199999)...) // 200000-byte ES → 4 PES chunks
	nalus := [][]byte{append([]byte{0x67}, mkNAL(12)...), idr}

	psData := MuxH264ToPS(nalus, true, time.Unix(1, 0), time.Unix(1, 0))
	pkts := walkPESPackets(t, psData)

	wantChunks := 4 // 65000 + 65000 + 65000 + remainder
	if len(pkts) != wantChunks {
		t.Fatalf("expected %d PES packets for a 200KB ES, got %d", wantChunks, len(pkts))
	}

	// Every chunk balanced and bounded; only the first carries timestamps.
	var es []byte
	for i, pkt := range pkts {
		if pkt.declared != pkt.actual {
			t.Errorf("PES %d: declared %d bytes but %d present — strict receivers stall (issue #15)", i, pkt.declared, pkt.actual)
		}
		if i == 0 {
			if !pkt.hasTS || pkt.headerLen != 10 {
				t.Errorf("first PES must carry PTS+DTS (headerLen 10), got hasTS=%v headerLen=%d", pkt.hasTS, pkt.headerLen)
			}
		} else if pkt.hasTS || pkt.headerLen != 0 {
			t.Errorf("continuation PES %d must not carry timestamps, got hasTS=%v headerLen=%d", i, pkt.hasTS, pkt.headerLen)
		}
		// Payload = prefix(6) + flags(1) + hdrlen(1) + optional(hdrLen) + gap(1).
		payloadStart := pkt.offset + 9 + pkt.headerLen
		pesBytes := psData[payloadStart:]
		es = append(es, pesBytes[:pkt.declared-3-pkt.headerLen]...)
	}

	// The split ES must reassemble to the exact pre-mux elementary stream.
	wantES := append(append([]byte{0x00, 0x00, 0x00, 0x01}, nalus[0]...), append([]byte{0x00, 0x00, 0x01}, nalus[1]...)...)
	if !bytes.Equal(es, wantES) {
		t.Errorf("split ES does not reassemble: got %d bytes, want %d", len(es), len(wantES))
	}
}

// TestMuxH264ToPSStrictReceiverReassembly replays the issue #15 wire path
// end-to-end: mux → 1400-byte RTP fragments (marker on last) → AU reassembly →
// strict PS walk. At the access-unit boundary NO PES may remain pending, and
// every NAL unit must be recovered — including the mid-AU flush case (jitter
// buffer force-flush feeds a partial AU before the marker completes it).
func TestMuxH264ToPSStrictReceiverReassembly(t *testing.T) {
	mkNAL := func(head byte, n int) []byte {
		b := make([]byte, n)
		x := uint32(0x23456789)
		for i := range b {
			x = x*1664525 + 1013904223
			b[i] = byte(0x01 + (x>>16)%255)
		}
		return append([]byte{head}, b...)
	}

	cases := []struct {
		name      string
		size      int
		keyframe  bool
		flushMidA bool
	}{
		{"3KB P-frame", 3000, false, false},
		{"16KB P-frame (issue #15 signature)", 16000, false, false},
		{"64KB+ keyframe", 70000, true, false},
		{"200KB keyframe", 200000, true, false},
		{"16KB P-frame with mid-AU flush", 16000, false, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var nalus [][]byte
			if tc.keyframe {
				nalus = append(nalus, mkNAL(0x67, 12), mkNAL(0x68, 4))
			}
			rem := tc.size
			for rem > 0 {
				n := 4096
				if rem < n {
					n = rem
				}
				nalus = append(nalus, mkNAL(0x65, n))
				rem -= n
			}
			pts := time.Unix(1_800_000_000, 0)
			psData := MuxH264ToPS(nalus, tc.keyframe, pts, pts)

			// rtp_pusher.SendFrame fragmentation profile (MTU 1400).
			const rtpMtu = 1400
			var au []byte
			for off := 0; off < len(psData); off += rtpMtu {
				end := off + rtpMtu
				if end > len(psData) {
					end = len(psData)
				}
				au = append(au, psData[off:end]...)
			}

			var pkts []pesPacket
			if tc.flushMidA {
				cut := len(au) / 2
				pending := false
				for _, p := range walkPESPackets(t, au[:cut]) {
					if p.declared > p.actual {
						pending = true
					}
				}
				// Mid-AU flush must leave a PES pending — the strict receiver
				// buffers it and completes on the next feed.
				if !pending {
					t.Fatalf("mid-AU flush should leave a PES pending (strict receiver would buffer it)")
				}
				// The buffered bytes are prepended to the next feed, so the
				// completed AU parses exactly like the full-burst walk below.
				pkts = walkPESPackets(t, au)
			} else {
				pkts = walkPESPackets(t, au)
			}

			// Contract: at the AU marker, no PES may still be incomplete.
			for i, p := range pkts {
				if p.declared > p.actual {
					t.Fatalf("AU ended mid-PES: PES %d declares %d bytes, %d arrived — receiver drops the frame (issue #15)",
						i, p.declared, p.actual)
				}
			}

			// And the full AU must be accounted for: walking by declared
			// lengths must land exactly on the end of the burst.
			last := pkts[len(pkts)-1]
			if got, want := last.offset+6+last.declared, len(au); got != want {
				t.Fatalf("strict walk consumed %d of %d AU bytes — %d bytes of ES lost or smeared into the next AU",
					got, want, want-got)
			}
		})
	}
}

// extractNALUs extracts NAL units from Annex-B concatenated data.
// Returns raw NAL data without start codes.
func extractNALUs(data []byte) [][]byte {
	startCode4 := []byte{0x00, 0x00, 0x00, 0x01}
	startCode3 := []byte{0x00, 0x00, 0x01}

	var nalus [][]byte
	offset := 0

	for offset < len(data) {
		// Find next start code
		var pos int

		if bytes.HasPrefix(data[offset:], startCode4) {
			pos = offset + 4
		} else if bytes.HasPrefix(data[offset:], startCode3) {
			pos = offset + 3
		} else {
			// No valid start code found
			break
		}

		// Find end of this NAL (next start code or end of data)
		endPos := len(data)
		if i := bytes.Index(data[pos:], startCode4); i != -1 {
			endPos = pos + i
		} else if i := bytes.Index(data[pos:], startCode3); i != -1 {
			endPos = pos + i
		}

		// Extract NAL data
		if endPos > pos {
			nalus = append(nalus, data[pos:endPos])
		}

		offset = endPos
	}

	return nalus
}
