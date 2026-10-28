//go:build gb35114

// GB 35114 B/C media security over PS-on-RTP: per-AU SM2 signatures (B) and
// SM4-OFB payload encryption under a VKEK-sealed VEK (C).
//
// The standard builds these on SVAC (GB/T 25724) streams; this software
// form keeps the standard's algorithms and key hierarchy and defines the
// two wire details the standard leaves to the SVAC codec:
//
//   - the VEK travels as an SM4-ECB envelope under the negotiated VKEK
//     (the standard's key-negotiation-data protection) inside the INVITE
//     SDP `a=security:` attribute;
//   - each RTP packet is XORed with an SM4-OFB keystream seeded by
//     IV = SM3(VEK ‖ SSRC ‖ seq)[0:16], so packet loss never desyncs the
//     keystream (a continuous OFB chain would lose the rest of the stream
//     on the first dropped packet).

package security35114

import (
	"crypto/cipher"
	"crypto/ecdsa"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/emmansun/gmsm/sm2"
	"github.com/emmansun/gmsm/sm3"
	"github.com/emmansun/gmsm/sm4"
	"github.com/emmansun/gmsm/smx509"
)

// VEKLen is the media session key length in bytes (128-bit VEK, §"运算").
const VEKLen = 16

// GenerateVEK draws a fresh 128-bit video-encrypting key.
func GenerateVEK(r io.Reader) ([]byte, error) {
	if r == nil {
		r = rand.Reader
	}
	vek := make([]byte, VEKLen)
	if _, err := io.ReadFull(r, vek); err != nil {
		return nil, fmt.Errorf("security35114: drawing VEK: %w", err)
	}
	return vek, nil
}

// SealVEK wraps a VEK with SM4-ECB under the session VKEK — the key
// envelope. A 16-byte key under a 16-byte block cipher is exactly one
// block, so no padding is defined or needed.
func SealVEK(vkek, vek []byte) (string, error) {
	if len(vkek) != sm4.BlockSize {
		return "", fmt.Errorf("security35114: VKEK is %d bytes, want %d", len(vkek), sm4.BlockSize)
	}
	if len(vek) != VEKLen {
		return "", fmt.Errorf("security35114: VEK is %d bytes, want %d", len(vek), VEKLen)
	}
	block, err := sm4.NewCipher(vkek)
	if err != nil {
		return "", fmt.Errorf("security35114: SM4: %w", err)
	}
	out := make([]byte, VEKLen)
	block.Encrypt(out, vek)
	return base64.StdEncoding.EncodeToString(out), nil
}

// OpenVEK unwraps a SealVEK envelope.
func OpenVEK(vkek []byte, envelope string) ([]byte, error) {
	if len(vkek) != sm4.BlockSize {
		return nil, fmt.Errorf("security35114: VKEK is %d bytes, want %d", len(vkek), sm4.BlockSize)
	}
	raw, err := base64.StdEncoding.DecodeString(envelope)
	if err != nil {
		return nil, fmt.Errorf("security35114: VEK envelope is not base64: %w", err)
	}
	if len(raw) != VEKLen {
		return nil, fmt.Errorf("security35114: VEK envelope is %d bytes, want %d", len(raw), VEKLen)
	}
	block, err := sm4.NewCipher(vkek)
	if err != nil {
		return nil, fmt.Errorf("security35114: SM4: %w", err)
	}
	vek := make([]byte, VEKLen)
	block.Decrypt(vek, raw)
	return vek, nil
}

// PacketIV derives the per-RTP-packet SM4-OFB IV: SM3(VEK ‖ SSRC ‖ seq)
// truncated to 128 bits.
func PacketIV(vek []byte, ssrc uint32, seq uint16) []byte {
	h := sm3.New()
	h.Write(vek)
	var be [6]byte
	binary.BigEndian.PutUint32(be[:4], ssrc)
	binary.BigEndian.PutUint16(be[4:], seq)
	h.Write(be[:])
	return h.Sum(nil)[:sm4.BlockSize]
}

// PacketCryptor applies the level-C SM4-OFB payload protection for one
// session. The SM4 key schedule is expanded once at construction — per
// packet only the IV derivation and keystream XOR run. Zero wipes the key
// material when the session ends (best effort under Go's GC; call it from
// teardown paths).
type PacketCryptor struct {
	vek   []byte
	block cipher.Block
}

// NewPacketCryptor binds a cryptor to one VEK. Encryption and decryption
// are the same XOR operation.
func NewPacketCryptor(vek []byte) (*PacketCryptor, error) {
	if len(vek) != VEKLen {
		return nil, fmt.Errorf("security35114: VEK is %d bytes, want %d", len(vek), VEKLen)
	}
	block, err := sm4.NewCipher(vek)
	if err != nil {
		return nil, fmt.Errorf("security35114: SM4: %w", err)
	}
	c := &PacketCryptor{vek: append([]byte(nil), vek...), block: block}
	return c, nil
}

// Crypt XORs one RTP payload with the per-packet SM4-OFB keystream.
func (c *PacketCryptor) Crypt(payload []byte, ssrc uint32, seq uint16) []byte {
	if c == nil || len(payload) == 0 {
		return payload
	}
	stream := cipher.NewOFB(c.block, PacketIV(c.vek, ssrc, seq))
	out := make([]byte, len(payload))
	stream.XORKeyStream(out, payload)
	return out
}

// Zero wipes the VEK material (idempotent).
func (c *PacketCryptor) Zero() {
	if c == nil {
		return
	}
	for i := range c.vek {
		c.vek[i] = 0
	}
	c.vek = nil
}

// AUSignPayload builds the level-B signed octets: the reassembled PS access
// unit followed by the RTP binding (SSRC and the AU's RTP timestamp,
// big-endian) — one signature is bound to exactly one AU of one stream.
func AUSignPayload(au []byte, ssrc, timestamp uint32) []byte {
	out := make([]byte, 0, len(au)+8)
	out = append(out, au...)
	var be [8]byte
	binary.BigEndian.PutUint32(be[:4], ssrc)
	binary.BigEndian.PutUint32(be[4:], timestamp)
	return append(out, be[:]...)
}

// SignAU signs one access unit with SM2 and returns the DER signature
// carried in the RTP header extension of the AU's last packet.
func SignAU(priv *sm2.PrivateKey, au []byte, ssrc, timestamp uint32) ([]byte, error) {
	sig, err := sm2.SignASN1(rand.Reader, priv, AUSignPayload(au, ssrc, timestamp), sm2.NewSM2SignerOption(true, nil))
	if err != nil {
		return nil, fmt.Errorf("security35114: signing AU: %w", err)
	}
	return sig, nil
}

// VerifyAU verifies an AU signature against a certificate.
func VerifyAU(cert *smx509.Certificate, au []byte, ssrc, timestamp uint32, sigDER []byte) error {
	pub, ok := cert.PublicKey.(*ecdsa.PublicKey)
	if !ok {
		return fmt.Errorf("security35114: certificate public key is %T, want an SM2 (EC) key", cert.PublicKey)
	}
	if !sm2.VerifyASN1WithSM2(pub, nil, AUSignPayload(au, ssrc, timestamp), sigDER) {
		return errors.New("security35114: AU signature verification failed")
	}
	return nil
}

// SDPSecurityOffer is the INVITE-offer form: the requested level, no key
// material ("a=security:GB35114-C").
func SDPSecurityOffer(level Level) string {
	return "GB35114-" + string(level)
}

// SDPSecurityAnswer is the 200-OK-answer form: the echoed level plus, at
// level C, the VKEK-sealed VEK envelope and the VKEK version it was sealed
// under ("a=security:GB35114-C,vek=<b64>,vkekver=<n>"). vkekVer <= 0 omits
// the version (envelopes from peers that do not count handshakes).
func SDPSecurityAnswer(level Level, vekEnvelope string, vkekVer int) string {
	if level.AtLeast(LevelC) && vekEnvelope != "" {
		ans := "GB35114-" + string(level) + ",vek=" + vekEnvelope
		if vkekVer > 0 {
			ans += fmt.Sprintf(",vkekver=%d", vkekVer)
		}
		return ans
	}
	return "GB35114-" + string(level)
}

// SDPSecurity is a parsed a=security attribute.
type SDPSecurity struct {
	Level       Level
	VEKEnvelope string // level C answers only
	VKEKVer     int    // handshake generation the envelope was sealed under (0 = unversioned)
}

// ParseSDPSecurity parses the value of an "a=security:" SDP attribute:
// "GB35114-B", "GB35114-C,vek=<b64>" or "GB35114-C,vek=<b64>,vkekver=<n>".
func ParseSDPSecurity(value string) (SDPSecurity, error) {
	value = strings.TrimSpace(value)
	token, param, _ := strings.Cut(value, ",")
	token = strings.TrimSpace(token)
	prefix := "GB35114-"
	if !strings.HasPrefix(token, prefix) {
		return SDPSecurity{}, fmt.Errorf("security35114: not a GB35114 SDP security value: %q", value)
	}
	lvl, err := ParseLevel(strings.TrimPrefix(token, prefix))
	if err != nil {
		return SDPSecurity{}, fmt.Errorf("security35114: bad GB35114 SDP level: %q", value)
	}
	out := SDPSecurity{Level: lvl}
	for param != "" {
		var kv string
		kv, param, _ = strings.Cut(param, ",")
		k, v, _ := strings.Cut(strings.TrimSpace(kv), "=")
		switch k {
		case "vek":
			if v == "" {
				return SDPSecurity{}, fmt.Errorf("security35114: empty vek parameter")
			}
			out.VEKEnvelope = v
		case "vkekver":
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				return SDPSecurity{}, fmt.Errorf("security35114: bad vkekver %q", v)
			}
			out.VKEKVer = n
		default:
			return SDPSecurity{}, fmt.Errorf("security35114: bad a=security parameter %q", k)
		}
	}
	return out, nil
}
