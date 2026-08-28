package device

import (
	"bytes"
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

		// PES packet structure:
		// bytes 0-3: start code + stream ID
		// bytes 4-5: packet length
		// byte 6: flags
		// byte 7: header_data_length
		// bytes 8..: optional header (PTS/DTS)
		// then payload
		if pos+8 > len(psData) {
			break
		}
		// Get header_data_length (byte 7)
		headerDataLen := int(psData[pos+7])
		// Payload starts after 8 + headerDataLen bytes
		payloadPos := pos + 8 + headerDataLen
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
