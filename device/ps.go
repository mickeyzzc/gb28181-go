// implements MPEG-2 Program Stream (PS) muxing of
// H.264 access units into PS packets (PSM, PES) as required by
// GB/T 28181 RTP payload transport.

package device

import (
	"encoding/binary"
	"time"
)

// BuildPsPackHeader builds a PS pack header.
// Returns 14 bytes: 4-byte start code (0x000001BA) + 10 field bytes.
//
// scr: System Clock Reference in 27MHz ticks
// muxRate: Program mux rate in units of 50 bytes/sec (22-bit field)
//
// Reference: ISO/IEC 13818-1 §2.5.3.3, translated from
// notebook-cam/crates/protocols/src/gb28181/ps.rs build_ps_pack_header
func BuildPsPackHeader(scr uint64, muxRate uint32) []byte {
	header := make([]byte, 14)
	// Start code
	header[0] = 0x00
	header[1] = 0x00
	header[2] = 0x01
	header[3] = 0xBA

	// SCR base (33 bits) and extension (9 bits)
	base := scr / 300
	ext := scr % 300
	rate := muxRate & 0x3FFFFF // 22-bit program_mux_rate

	// data[4]: '01' MPEG-2 marker + SCR_base[32..30] + marker + SCR_base[29..28]
	header[4] = 0x44 | byte(((base>>30)&0x07)<<3) | byte((base>>28)&0x03)
	// data[5]: SCR_base[27..20]
	header[5] = byte((base >> 20) & 0xFF)
	// data[6]: SCR_base[19..15] + marker + SCR_base[14..13]
	header[6] = 0x04 | byte(((base>>15)&0x1F)<<3) | byte((base>>13)&0x03)
	// data[7]: SCR_base[12..5]
	header[7] = byte((base >> 5) & 0xFF)
	// data[8]: SCR_base[4..0] + marker + SCR_ext[8..7]
	header[8] = 0x04 | byte((base&0x1F)<<3) | byte((ext>>7)&0x03)
	// data[9]: SCR_ext[6..0] + marker
	header[9] = 0x01 | byte((ext&0x7F)<<1)
	// data[10..12]: program_mux_rate (22 bits) + two marker bits
	header[10] = byte(rate >> 14)
	header[11] = byte(rate >> 6)
	header[12] = 0x03 | byte((rate&0x3F)<<2)
	// data[13]: reserved '11111' + stuffing_length '000' (no stuffing)
	header[13] = 0xF8

	return header
}

// BuildProgramStreamMap builds a Program Stream Map (PSM).
// Returns 20 bytes: start code (00 00 01 BC) + length + two-byte version
// field + stream map + CRC-32/MPEG-2.
//
// Reference: ISO/IEC 13818-1 §2.5.3.5, translated from gb28181-rs
// build_program_stream_map (twin of gb28181-go #167 / gb28181-rs #111).
func BuildProgramStreamMap() []byte {
	// Body after the length field: version(2) + program_stream_info_length(2)
	// + elementary_stream_map_length(2) + stream entry(4) + CRC_32(4) = 14.
	body := make([]byte, 0, 14)
	// current_next_indicator(1)=1 + reserved(2)=11 + version(5)=00000 → 0xE0,
	// then reserved(7)=1111111 + marker(1)=1 → 0xFF (§2.5.3.5).
	body = append(body, 0xE0, 0xFF)

	// program_stream_info_length = 0 (no program stream info)
	body = append(body, 0x00, 0x00)

	// elementary_stream_map_length = 4 (one stream entry)
	body = append(body, 0x00, 0x04)

	// Stream entry: H.264 video
	body = append(body, 0x1B)       // stream_type: H.264 (MPEG-4 AVC)
	body = append(body, 0xE0)       // elementary_stream_id: video
	body = append(body, 0x00, 0x00) // es_info_length = 0

	// CRC_32 over everything after the length field (§2.5.3.5, Annex A)
	body = binary.BigEndian.AppendUint32(body, crc32MPEG(body))

	psm := append([]byte{0x00, 0x00, 0x01, 0xBC}, byte(len(body)>>8), byte(len(body)))
	return append(psm, body...)
}

// crc32MPEG computes CRC-32/MPEG-2 (poly 0x04C11DB7, init all-ones, no
// reflection) as required for the PSM CRC_32 field — identical to
// psmux.crc32MPEG.
func crc32MPEG(data []byte) uint32 {
	crc := uint32(0xFFFFFFFF)
	for _, b := range data {
		crc ^= uint32(b) << 24
		for range 8 {
			if crc&0x80000000 != 0 {
				crc = (crc << 1) ^ 0x04C11DB7
			} else {
				crc <<= 1
			}
		}
	}
	return crc
}

// encodePtsDts encodes a PTS or DTS value into 5 bytes per ISO/IEC 13818-1 2.4.3.7.
// prefix is the 4-bit timestamp prefix nibble: 2 for standalone PTS, 3 for PTS when DTS follows, 1 for DTS.
// value is the timestamp in 90kHz ticks.
func encodePtsDts(value uint64, prefix byte) [5]byte {
	return [5]byte{
		(prefix << 4) | byte(((value>>30)&0x07)<<1) | 0x01,
		byte((value >> 22) & 0xFF),
		byte(((value>>15)&0x7F)<<1) | 0x01,
		byte((value >> 7) & 0xFF),
		byte((value&0x7F)<<1) | 0x01,
	}
}

// BuildPesPacket builds a PES packet.
// Returns the PES packet bytes including the packet_start_code_prefix.
//
// streamID: Stream ID (0xE0 for video)
// payload: PES payload data (H.264 NAL units)
// pts: Presentation Time Stamp
// dts: Decode Time Stamp
//
// Layout (byte-identical to gb28181-rs build_pes_packet, the reference
// implementation validated against the NVR platform):
//
//	[00 00 01 streamID] [PES_packet_length] [0x80] [flags] [header_data_length]
//	[PTS/DTS optional fields] [payload]
//
// The header carries the standard THREE bytes (§2.4.3.6): the '10' flags
// byte (0x80 — no scrambling/alignment/copyright flags), PTS_DTS_flags, and
// PES_header_data_length; PES_packet_length counts those 3 header bytes:
// 3 + header_data_length + len(payload). The pre-#167 layout wrote the flags
// one byte early and compensated with a filler byte after the optional
// fields — byte count identical, but strict receivers (ffprobe/ZLMediaKit)
// read the header fields at the wrong offsets and reject the packet.
//
// Reference: ISO/IEC 13818-1 §2.4.3.6, translated from
// gb28181-rs/src/ps.rs build_pes_packet
func BuildPesPacket(streamID byte, payload []byte, pts, dts time.Time) []byte {
	pes := make([]byte, 0, 6+3+10+len(payload)) // Pre-allocate with space for worst case

	// Start code prefix
	pes = append(pes, 0x00, 0x00, 0x01, streamID)

	// Calculate optional header length
	var optionalHeaderLen byte
	hasPts := !pts.IsZero()
	hasDts := !dts.IsZero()
	if hasPts {
		optionalHeaderLen += 5
	}
	if hasDts {
		optionalHeaderLen += 5
	}

	// Packet length = 3 header bytes (flags + hdrlen + gap) + optional fields
	// + payload, with the 16-bit field's own cap honored: compute wide, then
	// fall back to unbounded (0) only for oversized payloads. MuxH264ToPS
	// splits large access units so its PES packets stay bounded.
	var packetLen uint16
	if optionalHeaderLen > 0 || len(payload) > 0 {
		if declared := uint32(3) + uint32(optionalHeaderLen) + uint32(len(payload)); declared <= 65535 {
			packetLen = uint16(declared)
		}
	}
	pes = append(pes, byte(packetLen>>8), byte(packetLen))

	// Standard 3-byte PES header (§2.4.3.6): the '10' flags byte first (no
	// scrambling, no alignment/copyright/original flags), then PTS_DTS_flags
	// (bits 7-6 of the second byte), then PES_header_data_length.
	var ptsDtsFlags byte
	switch {
	case hasPts && hasDts:
		ptsDtsFlags = 0b11
	case hasPts:
		ptsDtsFlags = 0b10
	case hasDts:
		ptsDtsFlags = 0b01 // Invalid in practice but allowed by spec
	default:
		ptsDtsFlags = 0b00
	}
	pes = append(pes, 0x80, ptsDtsFlags<<6, optionalHeaderLen)

	// Add PTS/DTS if present
	if hasPts {
		// PTS prefix nibble: '0011' when DTS follows, '0010' otherwise
		prefix := byte(0x2)
		if hasDts {
			prefix = 0x3
		}
		enc := encodePtsDts(timeTo90kHz(pts), prefix)
		pes = append(pes, enc[:]...)
	}
	if hasDts {
		enc := encodePtsDts(timeTo90kHz(dts), 0x1)
		pes = append(pes, enc[:]...)
	}

	// Add payload — at 9 + PES_header_data_length, the standard position.
	pes = append(pes, payload...)

	return pes
}

// maxPESChunkBytes bounds the elementary-stream bytes carried by one PES
// packet. PES_packet_length is a 16-bit field counting 3 header bytes +
// optional fields + payload (≤ 65535), so an access unit larger than ~64KB
// MUST be split across continuation PES packets — receivers accumulate the ES
// of one access unit across its PES packets. 65000 leaves headroom below the
// field's cap.
const maxPESChunkBytes = 65000

// MuxH264ToPS multiplexes H.264 NAL units into an MPEG-PS packet.
// Returns a complete PS pack including pack header, optional PSM, and PES packet.
//
// nalus: Slice of H.264 NAL unit byte slices
// isKeyFrame: Whether this is a key frame (IDR) - includes PSM on keyframes
// pts: Presentation Time Stamp
// dts: Decode Time Stamp
//
// CRITICAL: Emits pack header (0x000001BA) at start of EVERY access unit.
// Emits PSM only on keyframes (IDR).
// Packs multiple NALs into the PES elementary stream via Annex-B start-code
// concatenation (0x00000001), then splits the ES across bounded PES packets:
// the first carries PTS/DTS, continuation PES packets (access units larger
// than maxPESChunkBytes) carry none — the ES is continuous across the PES
// packets of one access unit.
//
// Reference: translated from gb28181-rs/src/ps.rs mux_h264_to_ps
func MuxH264ToPS(nalus [][]byte, isKeyFrame bool, pts, dts time.Time) []byte {
	ps := make([]byte, 0, 128) // Pre-allocate reasonable capacity

	// Convert PTS to 90kHz ticks
	pts90k := timeTo90kHz(pts)

	// Add pack header (EVERY access unit)
	// SCR from PTS (approximate), mux_rate at typical value
	scr := pts90k * 300      // Convert 90kHz to 27MHz
	muxRate := uint32(10000) // 50 bytes/sec units (adjust based on actual bitrate)
	ps = append(ps, BuildPsPackHeader(scr, muxRate)...)

	// Add PSM on keyframes only
	if isKeyFrame {
		ps = append(ps, BuildProgramStreamMap()...)
	}

	// Concatenate NAL units with Annex-B start codes
	payload := make([]byte, 0, len(nalus)*10) // Pre-allocate with headroom
	for i, nalu := range nalus {
		// Use 4-byte start code for all NALs (simpler, universally accepted)
		if i == 0 {
			payload = append(payload, 0x00, 0x00, 0x00, 0x01)
		} else {
			payload = append(payload, 0x00, 0x00, 0x01)
		}
		payload = append(payload, nalu...)
	}

	// Add PES packets: split the ES into bounded chunks. Only the first PES
	// carries PTS/DTS; a 16-bit PES_packet_length cannot describe an access
	// unit larger than ~64KB in one packet, and letting the field wrap
	// truncates every large IDR (issue #15).
	for start := 0; ; start += maxPESChunkBytes {
		end := start + maxPESChunkBytes
		if end > len(payload) {
			end = len(payload)
		}
		if start == 0 {
			ps = append(ps, BuildPesPacket(0xE0, payload[:end], pts, dts)...)
		} else {
			ps = append(ps, BuildPesPacket(0xE0, payload[start:end], time.Time{}, time.Time{})...)
		}
		if end == len(payload) {
			break
		}
	}

	return ps
}

// timeTo90kHz converts a time.Time to 90kHz timestamp ticks.
// Uses Unix epoch as reference: ticks = unix_nanos * 9 / 100_000
func timeTo90kHz(t time.Time) uint64 {
	if t.IsZero() {
		return 0
	}
	// Unix epoch in 90kHz: nanos * 9 / 100,000. The product is computed in
	// the unsigned domain — signed int64 overflow (nanos*9 exceeds 2^63 for
	// dates past 2026-05) would wrap the same bits, but the uint64 arithmetic
	// states the intent and keeps the mapping monotonic by construction.
	nanos := t.UnixNano()
	return uint64(nanos) * 9 / 100_000
}
