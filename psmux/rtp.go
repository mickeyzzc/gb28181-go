package psmux

import (
	"encoding/binary"
	"fmt"
	"net"
	"sync"

	"github.com/pion/rtp"
)

// RTPMTU is the max RTP payload size per packet — 1400 fits any Ethernet path.
const RTPMTU = 1400

// RTPPacketizer fragments a PS byte stream across RTP/UDP packets: PT 96,
// 90kHz timestamps, marker on the last packet of each PS burst (access unit).
type RTPPacketizer struct {
	conn       net.Conn
	dst        *net.UDPAddr // nil → TCP media (RFC 4571 framing)
	ssrc       uint32
	seq        uint16
	payloadTyp byte
	sent       uint64

	// mu serializes Send: one burst's packets must occupy a contiguous,
	// strictly increasing sequence-number block — cascade sessions call Send
	// for video AUs and audio frames from two hub callback goroutines, and an
	// unsynchronized seq++ interleaved bursts mid-packet-block (duplicate/
	// out-of-order seqs → the upper platform's contiguous drain broke → video
	// AUs truncated at PES-chunk boundaries; 2026-08-21).
	mu sync.Mutex

	// GB35114 B/C media security (both nil = the legacy hand-rolled wire
	// form, byte-identical to pre-security releases): payloadCryptor XORs
	// each fragment under the session VEK (level C); auSigner SM2-signs the
	// whole PS burst with the DER signature riding the marker packet as an
	// RFC 8285 two-byte-header extension (level B).
	payloadCryptor func(payload []byte, ssrc uint32, seq uint16) []byte
	auSigner       func(au []byte, ssrc uint32, tsTicks uint32) ([]byte, error)
}

// SetSecurity arms GB35114 media security on the packetizer. Called once at
// session setup (INVITE negotiation), before the first Send.
func (p *RTPPacketizer) SetSecurity(payloadCryptor func(payload []byte, ssrc uint32, seq uint16) []byte, auSigner func(au []byte, ssrc uint32, tsTicks uint32) ([]byte, error)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.payloadCryptor, p.auSigner = payloadCryptor, auSigner
}

func NewRTPPacketizer(conn net.Conn, dst *net.UDPAddr, ssrc uint32, initialSeq uint16) *RTPPacketizer {
	return &RTPPacketizer{conn: conn, dst: dst, ssrc: ssrc, seq: initialSeq, payloadTyp: 96}
}

// NewRTPPacketizerTCP builds a TCP-media packetizer: dst is nil, each RTP
// packet is framed with a 2-byte big-endian length prefix (RFC 4571 — the
// platform-side receiver auto-detects RFC4571 and 0x24 framings).
func NewRTPPacketizerTCP(conn net.Conn, ssrc uint32, initialSeq uint16) *RTPPacketizer {
	return &RTPPacketizer{conn: conn, ssrc: ssrc, seq: initialSeq, payloadTyp: 96}
}

// Send fragments and sends one PS burst; ts is the 90kHz AU timestamp.
// Atomic per burst: all fragments hold one contiguous seq block and the
// marker lands only on the final fragment.
func (p *RTPPacketizer) Send(ps []byte, tsTicks int64) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.payloadCryptor != nil || p.auSigner != nil {
		return p.sendSecured(ps, tsTicks)
	}
	return p.sendPlain(ps, tsTicks)
}

// sendPlain is the pre-security hand-rolled wire form.
func (p *RTPPacketizer) sendPlain(ps []byte, tsTicks int64) error {
	total := len(ps)
	for off := 0; off < total; off += RTPMTU {
		end := off + RTPMTU
		if end > total {
			end = total
		}
		marker := end == total
		pkt := make([]byte, 12, 12+end-off)
		pkt[0] = 0x80
		pkt[1] = p.payloadTyp
		if marker {
			pkt[1] |= 0x80
		}
		pkt[2] = byte(p.seq >> 8)
		pkt[3] = byte(p.seq)
		pkt[4] = byte(tsTicks >> 24)
		pkt[5] = byte(tsTicks >> 16)
		pkt[6] = byte(tsTicks >> 8)
		pkt[7] = byte(tsTicks)
		pkt[8] = byte(p.ssrc >> 24)
		pkt[9] = byte(p.ssrc >> 16)
		pkt[10] = byte(p.ssrc >> 8)
		pkt[11] = byte(p.ssrc)
		pkt = append(pkt, ps[off:end]...)
		p.seq++
		if p.dst != nil {
			if _, err := p.conn.(*net.UDPConn).WriteToUDP(pkt, p.dst); err != nil {
				return fmt.Errorf("psmux: rtp send: %w", err)
			}
		} else {
			// TCP media: RFC 4571 framing (2-byte length prefix).
			framed := make([]byte, 2+len(pkt))
			binary.BigEndian.PutUint16(framed, uint16(len(pkt)))
			copy(framed[2:], pkt)
			if _, err := p.conn.Write(framed); err != nil {
				return fmt.Errorf("psmux: rtp send (tcp): %w", err)
			}
		}
		p.sent++
	}
	return nil
}

// sendSecured is the GB35114 wire form: pion-built packets so the DER AU
// signature can ride the marker fragment as an RFC 8285 two-byte-header
// extension; each fragment's payload is XORed under the session VEK
// (per-packet IVs — loss of one fragment never desyncs the keystream).
func (p *RTPPacketizer) sendSecured(ps []byte, tsTicks int64) error {
	var sigDER []byte
	if p.auSigner != nil {
		sig, err := p.auSigner(ps, p.ssrc, uint32(tsTicks))
		if err != nil {
			return fmt.Errorf("psmux: rtp sign: %w", err)
		}
		sigDER = sig
	}
	total := len(ps)
	for off := 0; off < total; off += RTPMTU {
		end := off + RTPMTU
		if end > total {
			end = total
		}
		marker := end == total
		frag := ps[off:end]
		if p.payloadCryptor != nil {
			frag = p.payloadCryptor(frag, p.ssrc, p.seq)
		}
		packet := &rtp.Packet{
			Header: rtp.Header{
				Version:        2,
				PayloadType:    uint8(p.payloadTyp),
				Marker:         marker,
				SequenceNumber: p.seq,
				Timestamp:      uint32(tsTicks),
				SSRC:           p.ssrc,
			},
			Payload: frag,
		}
		if marker && sigDER != nil {
			packet.Header.ExtensionProfile = rtp.ExtensionProfileTwoByte
			if err := packet.SetExtension(1, sigDER); err != nil {
				return fmt.Errorf("psmux: rtp extension: %w", err)
			}
		}
		pkt, err := packet.Marshal()
		if err != nil {
			return fmt.Errorf("psmux: rtp marshal: %w", err)
		}
		if p.dst != nil {
			if _, err := p.conn.(*net.UDPConn).WriteToUDP(pkt, p.dst); err != nil {
				return fmt.Errorf("psmux: rtp send: %w", err)
			}
		} else {
			framed := make([]byte, 2+len(pkt))
			binary.BigEndian.PutUint16(framed, uint16(len(pkt)))
			copy(framed[2:], pkt)
			if _, err := p.conn.Write(framed); err != nil {
				return fmt.Errorf("psmux: rtp send (tcp): %w", err)
			}
		}
		p.seq++
		p.sent++
	}
	return nil
}

// Sent returns the number of RTP packets transmitted (diagnostics).
func (p *RTPPacketizer) Sent() uint64 { return p.sent }
