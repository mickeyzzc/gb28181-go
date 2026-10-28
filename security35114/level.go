//go:build gb35114

// GB 35114-2017 security levels. The standard assigns the media-side B/C
// features to SVAC (GB/T 25724) streams; this library implements their
// cryptographic essence for PS-over-RTP media instead:

//
//	Level A — SM2-certificate mutual authentication + keyed-SM3 Note
//	          integrity on device→platform requests (§9.4).
//	Level B — A + per-access-unit video signatures (the software form of
//	          "视频数据签名": the device SM2-signs every reassembled AU, the
//	          platform verifies against the device certificate) and Note
//	          integrity on platform→device requests too (the standard makes
//	          the digest bidirectional; level B is where this stack turns it
//	          on).
//	Level C — B + media encryption: a per-session 128-bit VEK distributed
//	          as an SM4-ECB envelope under the negotiated VKEK (the standard's
//	          key-negotiation-data protection) and SM4-OFB RTP-payload
//	          encryption (the standard's video-data protection), negotiated
//	          through the INVITE SDP.

package security35114

import "errors"

// Level is the GB 35114 security level a side operates at.
type Level string

const (
	// LevelA is authentication + keyed signaling integrity.
	LevelA Level = "A"
	// LevelB adds per-AU video signatures and bidirectional Note integrity.
	LevelB Level = "B"
	// LevelC adds SM4-OFB media encryption under a VKEK-sealed VEK.
	LevelC Level = "C"
)

// ParseLevel maps config strings onto Level (case-insensitive; "" is A).
func ParseLevel(s string) (Level, error) {
	switch s {
	case "", "A", "a":
		return LevelA, nil
	case "B", "b":
		return LevelB, nil
	case "C", "c":
		return LevelC, nil
	}
	return "", errors.New("security35114: level must be A, B or C")
}

// AtLeast reports whether the level covers min (A ⊂ B ⊂ C).
func (l Level) AtLeast(want Level) bool {
	order := map[Level]int{LevelA: 0, LevelB: 1, LevelC: 2}
	return order[l] >= order[want]
}
