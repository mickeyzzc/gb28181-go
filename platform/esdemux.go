package platform

// ES-over-RTP depacketization (issue #110): GB/T 28181-2022 allows the
// media channel to carry bare H.264/H.265 elementary streams over RTP
// (RFC 6184 / RFC 7798) instead of MPEG-PS. The Receiver classifies the
// first payload of a session (PS starts with a 00 00 01 Bx start code;
// ES starts with a NAL header) and feeds ES sessions through this
// depacketizer, which produces the same [][]byte access-unit shape the
// PS path emits — FrameHub consumers and the AU/NALU callbacks do not
// change.
//
// Packetization modes (per packet): H.264 single NALU (types 1-23),
// STAP-A (24), FU-A (28); H.265 single NALU (type < 48), AP (48),
// FU (49). The AU boundary is the RTP marker bit, matching the PS
// path's burst model: NALUs collect until the marker, then the AU is
// taken. A lost FU start drops the fragments (the gap path also calls
// DropPartial) — a hole is corruption, not a smaller frame.

import (
	"errors"
	"fmt"
)

// ESDemuxer reassembles RFC 6184 / RFC 7798 payloads into NALUs.
type ESDemuxer struct {
	h265 bool

	nalus [][]byte // NALUs collected for the AU in flight
	fu    []byte   // FU reassembly buffer (carries the reconstructed header)
	inFU  bool
}

// NewESDemuxer returns an ES depacketizer (codec latched from the first
// payload).
func NewESDemuxer() *ESDemuxer {
	return &ESDemuxer{}
}

// Codec reports the latched codec ("h264" / "h265").
func (d *ESDemuxer) Codec() string {
	if d.h265 {
		return "h265"
	}
	return "h264"
}

// TakeAU returns and clears the NALUs collected since the last take.
func (d *ESDemuxer) TakeAU() [][]byte {
	au := d.nalus
	d.nalus = nil
	return au
}

// DropPartial clears the in-flight reassembly state (packet loss / gap).
func (d *ESDemuxer) DropPartial() {
	d.fu = nil
	d.inFU = false
	d.nalus = nil
}

// looksLikeESPacket reports whether a payload plausibly opens an
// elementary stream (used once per session; PS start codes win first).
func looksLikeESPacket(p []byte) bool {
	if len(p) == 0 || p[0]&0x80 != 0 {
		return false // forbidden_zero must be clear
	}
	if p[0] == 0 && len(p) > 1 && p[1] == 0 {
		return false // start-code-ish, not a NAL header
	}
	h264t := p[0] & 0x1F
	if h264t >= 1 && h264t <= 23 || h264t == 24 || h264t == 28 {
		return true
	}
	// H.265 parameter sets / aggregates read as reserved H.264 types
	// (e.g. VPS 0x40 -> type 0, AP 0x60/0x62 -> 0/2) — accept them.
	h265t := (p[0] >> 1) & 0x3F
	return h265t >= 32 && h265t <= 49
}

// FeedPacket processes one RTP packet's ES payload.
func (d *ESDemuxer) FeedPacket(p []byte) error {
	if len(p) == 0 {
		return nil
	}
	if p[0]&0x80 != 0 {
		return errors.New("es: forbidden_zero bit set")
	}

	if !d.h265 {
		t := p[0] & 0x1F
		switch {
		case t >= 1 && t <= 23:
			d.nalus = append(d.nalus, append([]byte{}, p...))
			return nil
		case t == 24:
			return d.feedSTAPA(p)
		case t == 28:
			return d.feedFUA(p)
		default:
			// Reserved/MIC types 0, 25-27, 29-31 are not H.264. H.265
			// parameter sets (VPS/SPS/PPS/AUD, types 32-35) and the
			// aggregates (AP 48 / FU 49) all land here — latch H.265 and
			// reparse.
			if !d.tryLatchH265(p) {
				return fmt.Errorf("es: unsupported H.264 NAL type %d", t)
			}
			return d.feedH265(p)
		}
	}
	return d.feedH265(p)
}

// tryLatchH265 reports whether the packet is only valid as H.265 —
// parameter-set types 32..47 and the aggregates (AP 48 / FU 49) all
// read as reserved H.264 types — and latches it.
func (d *ESDemuxer) tryLatchH265(p []byte) bool {
	t := (p[0] >> 1) & 0x3F
	if t >= 32 && t <= 49 {
		d.h265 = true
		return true
	}
	return false
}

func (d *ESDemuxer) feedSTAPA(p []byte) error {
	// p[0] = STAP-A header; then [u16 size | NALU] entries.
	off := 1
	for off < len(p) {
		if off+2 > len(p) {
			return errors.New("es: STAP-A truncated size field")
		}
		size := int(p[off])<<8 | int(p[off+1])
		off += 2
		if size <= 0 || off+size > len(p) {
			return fmt.Errorf("es: STAP-A entry size %d overruns payload", size)
		}
		d.nalus = append(d.nalus, append([]byte{}, p[off:off+size]...))
		off += size
	}
	return nil
}

func (d *ESDemuxer) feedFUA(p []byte) error {
	if len(p) < 2 {
		return errors.New("es: FU-A packet without FU header")
	}
	fuHdr := p[1]
	start := fuHdr&0x80 != 0
	end := fuHdr&0x40 != 0
	switch {
	case start:
		if end {
			return errors.New("es: FU-A header has both S and E")
		}
		hdr := (p[0] & 0xE0) | (fuHdr & 0x1F)
		d.fu = append([]byte{hdr}, p[2:]...)
		d.inFU = true
	case d.inFU:
		d.fu = append(d.fu, p[2:]...)
	default:
		return errors.New("es: FU-A continuation without a start")
	}
	if end && d.inFU {
		d.nalus = append(d.nalus, d.fu)
		d.fu = nil
		d.inFU = false
	}
	return nil
}

func (d *ESDemuxer) feedH265(p []byte) error {
	if len(p) < 2 {
		return errors.New("es: H.265 packet shorter than the 2-byte NAL header")
	}
	t := (p[0] >> 1) & 0x3F
	switch {
	case t < 48:
		d.nalus = append(d.nalus, append([]byte{}, p...))
		return nil
	case t == 48: // AP
		off := 2
		for off < len(p) {
			if off+2 > len(p) {
				return errors.New("es: H.265 AP truncated size field")
			}
			size := int(p[off])<<8 | int(p[off+1])
			off += 2
			if size <= 0 || off+size > len(p) {
				return fmt.Errorf("es: H.265 AP entry size %d overruns payload", size)
			}
			d.nalus = append(d.nalus, append([]byte{}, p[off:off+size]...))
			off += size
		}
		return nil
	case t == 49: // FU
		if len(p) < 3 {
			return errors.New("es: H.265 FU without FU header")
		}
		fuHdr := p[2]
		start := fuHdr&0x80 != 0
		end := fuHdr&0x40 != 0
		type6 := fuHdr & 0x3F
		switch {
		case start:
			if end {
				return errors.New("es: H.265 FU header has both S and E")
			}
			hdr0 := (p[0] & 0x81) | (type6 << 1)
			d.fu = append([]byte{hdr0, p[1]}, p[3:]...)
			d.inFU = true
		case d.inFU:
			d.fu = append(d.fu, p[3:]...)
		default:
			return errors.New("es: H.265 FU continuation without a start")
		}
		if end && d.inFU {
			d.nalus = append(d.nalus, d.fu)
			d.fu = nil
			d.inFU = false
		}
		return nil
	default:
		return fmt.Errorf("es: unsupported H.265 NAL type %d", t)
	}
}
