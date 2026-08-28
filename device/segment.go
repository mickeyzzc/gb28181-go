// Reference recording-segment format: bare Annex-B H.264 with a per-frame
// `<segment>.ts.jsonl` sidecar of millisecond PTS offsets. This is the
// on-disk format the playback/download path reads ([OpenSegment]); hosts
// that record with it get RecordInfo/playback support without a format
// shim.

package device

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

// nominalFPS is the fallback frame rate used when a segment has no
// sidecar (legacy or hand-made files).
const nominalFPS = 25

// SegmentReader yields access-unit-shaped frames from a recorded
// Annex-B segment file, pairing each frame with its recorded PTS from
// the .ts.jsonl sidecar. If the sidecar is missing, it falls back to a
// nominal 25fps pacing.
type SegmentReader struct {
	aus     [][]NALU // access units, in order
	pts     []time.Duration
	nominal bool // true when using nominal fps fallback
	idx     int
}

// OpenSegment parses an Annex-B segment file into access units and
// loads its PTS sidecar.
func OpenSegment(file string) (*SegmentReader, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("reading segment %s: %w", file, err)
	}
	parser := &annexBParser{}
	nalus := parser.Parse(data)

	var aus [][]NALU
	for _, n := range nalus {
		if len(aus) == 0 || startsNewAU(aus[len(aus)-1], n) {
			aus = append(aus, []NALU{n})
		} else {
			aus[len(aus)-1] = append(aus[len(aus)-1], n)
		}
	}

	pts, ok := loadSidecar(file + ".ts.jsonl")
	return &SegmentReader{
		aus:     aus,
		pts:     pts,
		nominal: !ok,
	}, nil
}

// isVCL reports whether a NALU carries coded slice data (types 1 and 5).
func isVCL(n NALU) bool { return n.Type == 1 || n.Type == 5 }

// startsNewAU reports whether n begins a new access unit given the
// current (in-progress) access unit cur. A new AU starts at a VCL
// NALU (slice) when the current AU already contains a VCL NALU.
// Non-VCL NALUs (SPS/PPS/SEI/AUD) attach to the following VCL NALU's
// AU, so a segment's leading SPS+PPS+IDR group into one AU.
func startsNewAU(cur []NALU, n NALU) bool {
	if !isVCL(n) {
		return false
	}
	for _, c := range cur {
		if isVCL(c) {
			return true
		}
	}
	return false
}

// loadSidecar reads a .ts.jsonl sidecar into a slice of PTS offsets.
// Returns ok=false if the file is missing or unreadable.
func loadSidecar(path string) ([]time.Duration, bool) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer f.Close()

	var pts []time.Duration
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var rec struct {
			PTSMS int64 `json:"pts_ms"`
		}
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}
		pts = append(pts, time.Duration(rec.PTSMS)*time.Millisecond)
	}
	if len(pts) == 0 {
		return nil, false
	}
	return pts, true
}

// Next returns the next access unit as raw NALU byte slices (without
// start codes), its PTS offset relative to the segment start, and
// whether it is a keyframe. It returns io.EOF when exhausted.
func (r *SegmentReader) Next() (naluBytes [][]byte, ptsOffset time.Duration, isKeyFrame bool, err error) {
	if r.idx >= len(r.aus) {
		return nil, 0, false, errEOF
	}
	au := r.aus[r.idx]
	naluBytes = make([][]byte, len(au))
	for i, n := range au {
		naluBytes[i] = n.Data
		if n.IsIDR {
			isKeyFrame = true
		}
	}

	if r.nominal {
		ptsOffset = time.Duration(r.idx) * time.Second / nominalFPS
	} else if r.idx < len(r.pts) {
		ptsOffset = r.pts[r.idx]
	} else {
		// Sidecar shorter than the parsed AUs — fall back to nominal.
		ptsOffset = time.Duration(r.idx) * time.Second / nominalFPS
	}

	r.idx++
	return naluBytes, ptsOffset, isKeyFrame, nil
}

// errEOF is returned by Next when the segment is exhausted.
var errEOF = fmt.Errorf("device: end of segment")

// SegmentEOF reports whether err is the end-of-segment sentinel.
func SegmentEOF(err error) bool { return err == errEOF }

// ---------------------------------------------------------------------------
// Annex-B parser (start-code scan only)
// ---------------------------------------------------------------------------

// annexBParser splits H.264 Annex-B bytestreams into NALUs.
type annexBParser struct{}

// Parse splits Annex-B data into individual NALUs.
// Start codes: 0x00000001 (4-byte) or 0x000001 (3-byte).
func (p *annexBParser) Parse(data []byte) []NALU {
	if len(data) == 0 {
		return nil
	}

	positions := p.FindStartCodes(data)
	if len(positions) == 0 {
		return nil
	}

	nalus := make([]NALU, 0, len(positions))

	for i, pos := range positions {
		// Determine the NALU data start (skip start code).
		var naluStart int
		if pos+4 <= len(data) && data[pos] == 0 && data[pos+1] == 0 && data[pos+2] == 0 && data[pos+3] == 1 {
			naluStart = pos + 4
		} else {
			naluStart = pos + 3
		}

		if naluStart >= len(data) {
			break
		}

		// Find the end: next start code or end of data.
		var naluEnd int
		if i+1 < len(positions) {
			naluEnd = positions[i+1]
		} else {
			naluEnd = len(data)
		}

		naluData := data[naluStart:naluEnd]
		if len(naluData) == 0 {
			continue
		}

		naluType := naluData[0] & 0x1F

		nalus = append(nalus, NALU{
			Type:  naluType,
			Data:  naluData,
			IsIDR: naluType == 5,
			IsSPS: naluType == 7,
			IsPPS: naluType == 8,
		})
	}

	return nalus
}

// FindStartCodes returns indices of all start code positions in data.
// Matches both 4-byte (0x00000001) and 3-byte (0x000001) start codes.
func (p *annexBParser) FindStartCodes(data []byte) []int {
	if len(data) < 3 {
		return nil
	}

	var positions []int
	i := 0

	for i < len(data)-2 {
		// Look for 0x000001 pattern (the core of both 3-byte and 4-byte start codes).
		if data[i] == 0 && data[i+1] == 0 && data[i+2] == 1 {
			// Check if preceded by 0x00 → 4-byte start code at i-1.
			if i > 0 && data[i-1] == 0 {
				positions = append(positions, i-1)
			} else {
				positions = append(positions, i)
			}
			i += 3
			continue
		}
		i++
	}

	return positions
}
