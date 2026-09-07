//go:build gb35114

// Package security35114 implements the GB 35114-2017 A-level device-side
// security capability for GB/T 28181: SM2-certificate mutual authentication
// during REGISTER and the keyed-SM3 integrity header for subsequent SIP
// signaling.
//
// Only level A is implemented. Levels B/C additionally require signed and
// encrypted media built on GB/T 25724 (SVAC), which is a hardware codec —
// out of scope by design.
//
// Build with the gb35114 tag:
//
//	go build -tags gb35114 ./...
//
// Wire formats follow the published standard text cross-checked against
// real device↔platform captures. Two points are ambiguous in the wild and
// are therefore configurable (see RandomEncoding and Sign2Order): the
// representation of the randoms inside the signed payload, and the R1/R2
// order of the platform's sign2 input.
package security35114

// CapabilityAlgorithm is the algorithm capability string announced in the
// Capability Authorization of the first REGISTER. The library advertises
// SM4 for stream ciphers (SM1 is hardware-only).
const CapabilityAlgorithm = "A:SM2;H:SM3;S:SM4/OFB/PKCS5;SI:SM3-SM2"

// RandomEncoding selects how the random values enter the signed payload.
//
// Real-world captures concatenate the base64 strings exactly as they appear
// in the headers, while a literal reading of the standard concatenates the
// decoded 16-byte randoms. Both are provided; the default matches captures.
type RandomEncoding int

const (
	// ConcatWireStrings concatenates the base64 header values (default).
	ConcatWireStrings RandomEncoding = iota
	// ConcatRawBytes concatenates the decoded random bytes.
	ConcatRawBytes
)

// Sign2Order selects the operand order of the platform's sign2 payload.
// The standard text reads R1+R2+deviceid+cryptkey; one widely cited capture
// concatenates R2 first. The default follows the standard text.
type Sign2Order int

const (
	// Sign2R1R2 signs random1+random2+deviceid+cryptkey (standard order).
	Sign2R1R2 Sign2Order = iota
	// Sign2R2R1 signs random2+random1+deviceid+cryptkey (capture order).
	Sign2R2R1
)

// VKEKEncoding selects how the negotiated VKEK enters the Note-header
// digest input.
type VKEKEncoding int

const (
	// VKEKRaw uses the raw 16-byte VKEK (default).
	VKEKRaw VKEKEncoding = iota
	// VKEKBase64String uses its base64 text form.
	VKEKBase64String
)
