// implements RTP packetization of PS streams and
// UDP/TCP push to the GB/T 28181 platform using github.com/pion/rtp.

package device

import (
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/mickeyzzc/gb28181-go/metrics"

	"github.com/pion/rtp"
)

// RtpPusher manages RTP packetization and UDP/TCP transmission of PS streams.
//
// SSRC is provided by the platform via INVITE SDP y= field (10-digit decimal),
// NOT derived from device_id+channel_id.
type RtpPusher struct {
	conn       *net.UDPConn
	remoteAddr *net.UDPAddr
	tcpConn    *net.TCPConn
	seqNum     uint16
	mu         sync.Mutex

	// GB35114 B/C media security (nil = plaintext session):
	// payloadEncryptor XORs each fragment's payload under the session VEK
	// (level C); auSigner SM2-signs the whole PS access unit, the signature
	// riding the AU's last packet as an RTP header extension (level B).
	payloadEncryptor RTPPayloadCryptor
	auSigner         AUSignerFunc

	// metrics receives PSBytesOut for every frame handed to the wire
	// (issue #40); nil = no-op.
	metrics metrics.Hooks
}

// RTPExtAUSignature is the RFC 8285 extension ID carrying the GB35114
// level-B per-AU SM2 signature on the AU's last (marker) packet. DER
// signatures exceed the 16-byte one-byte-header limit, so the two-byte
// header profile is used.
const RTPExtAUSignature = 1

// SetPayloadEncryptor arms level-C SM4-OFB payload encryption (nil clears).
func (rp *RtpPusher) SetPayloadEncryptor(fn RTPPayloadCryptor) {
	rp.mu.Lock()
	defer rp.mu.Unlock()
	rp.payloadEncryptor = fn
}

// SetAUSigner arms level-B per-AU signatures (nil clears).
func (rp *RtpPusher) SetAUSigner(fn AUSignerFunc) {
	rp.mu.Lock()
	defer rp.mu.Unlock()
	rp.auSigner = fn
}

// NewRtpPusher creates a new RTP pusher with the provided UDP connection.
// Initializes with a random sequence number.
func NewRtpPusher(conn *net.UDPConn, remoteAddr *net.UDPAddr) *RtpPusher {
	return &RtpPusher{
		conn:       conn,
		remoteAddr: remoteAddr,
		seqNum:     uint16(time.Now().UnixNano() & 0xFFFF), // Random init
	}
}

// SetTCPConn sets the TCP connection for framed RTP transport.
// When set, SendFrame will use $-framing (GB/T 28181 Annex C.2).
// SetMetricsHooks installs observability hooks (issue #40); nil-safe.
func (rp *RtpPusher) SetMetricsHooks(h metrics.Hooks) {
	rp.metrics = h
}

func (rp *RtpPusher) SetTCPConn(conn *net.TCPConn) {
	rp.mu.Lock()
	defer rp.mu.Unlock()
	rp.tcpConn = conn
}

// BuildRtpPacket builds an RTP packet using pion/rtp.
// Returns the marshaled packet bytes ready for UDP transmission.
//
// payload: PS payload data
// marker: Marker bit (true only on last fragment of an access unit)
// ssrc: Platform-provided SSRC from INVITE SDP y= field
// seqNum: RTP sequence number
// timestamp: RTP timestamp in 90kHz ticks
func BuildRtpPacket(payload []byte, marker bool, ssrc uint32, seqNum uint16, timestamp uint32) ([]byte, error) {
	packet := &rtp.Packet{
		Header: rtp.Header{
			Version:        2,  // RTP version 2
			PayloadType:    96, // PS over RTP per GB/T 28181
			Marker:         marker,
			SequenceNumber: seqNum,
			Timestamp:      timestamp,
			SSRC:           ssrc,
		},
		Payload: payload,
	}
	buf, err := packet.Marshal()
	if err != nil {
		return nil, fmt.Errorf("failed to marshal RTP packet: %w", err)
	}
	return buf, nil
}

// BuildRtpPacketWithExtension builds an RTP packet carrying one RFC 8285
// header extension (the GB35114 level-B AU signature). DER SM2 signatures
// exceed the 16-byte one-byte-header limit, so the two-byte header profile
// is selected before the extension is attached.
func BuildRtpPacketWithExtension(payload []byte, marker bool, ssrc uint32, seqNum uint16, timestamp uint32, extID uint8, ext []byte) ([]byte, error) {
	packet := &rtp.Packet{
		Header: rtp.Header{
			Version:          2,
			PayloadType:      96,
			Marker:           marker,
			SequenceNumber:   seqNum,
			Timestamp:        timestamp,
			SSRC:             ssrc,
			ExtensionProfile: rtp.ExtensionProfileTwoByte,
		},
		Payload: payload,
	}
	if err := packet.SetExtension(extID, ext); err != nil {
		return nil, fmt.Errorf("failed to set RTP extension %d: %w", extID, err)
	}
	buf, err := packet.Marshal()
	if err != nil {
		return nil, fmt.Errorf("failed to marshal RTP packet: %w", err)
	}
	return buf, nil
}

// writeRTPOverTCP frames an RTP packet with GB/T 28181 $-framing.
// RTSP-interleaved style (Annex C.2 as demuxed by GB28181 platforms and
// ZLMediaKit/wvp-class receivers):
// [0x24 '$'] [channel 0x00] [2-byte big-endian length] [RTP packet bytes]
func writeRTPOverTCP(conn net.Conn, rtpPkt []byte) error {
	frame := make([]byte, 4+len(rtpPkt))
	frame[0] = 0x24 // '$' framing byte
	frame[1] = 0x00 // channel
	frame[2] = byte(len(rtpPkt) >> 8)
	frame[3] = byte(len(rtpPkt))
	copy(frame[4:], rtpPkt)
	_, err := conn.Write(frame)
	return err
}

const (
	// rtpMtu is the maximum payload size per RTP packet
	rtpMtu = 1400
)

// SendFrame sends a PS frame (from MuxH264ToPS) over RTP to the platform.
//
// psData: Already-muxed PS bytes (pack header + optional PSM + PES packet)
// isKeyFrame: Whether this is a key frame (IDR)
// pts: Presentation timestamp for RTP timestamp calculation
// ssrc: Platform-provided SSRC from INVITE SDP y= field
//
// The PS data is fragmented across multiple RTP packets at MTU 1400.
// The marker bit is set to true ONLY on the last fragment of the access unit.
// Thread-safe (mutex protects sequence number increment).
//
// Returns error on UDP/TCP send failure.
func (rp *RtpPusher) SendFrame(psData []byte, isKeyFrame bool, pts time.Time, ssrc uint32) error {
	rp.mu.Lock()
	defer rp.mu.Unlock()

	// Convert PTS to 90kHz RTP timestamp (32-bit per RFC 3550; wraps naturally)
	timestamp := uint32(timeTo90kHz(pts))

	// Level B: one signature over the whole PS access unit, carried in the
	// RTP header extension of the AU's final packet.
	var auSig []byte
	if rp.auSigner != nil {
		sig, err := rp.auSigner(psData, ssrc, timestamp)
		if err != nil {
			return fmt.Errorf("failed to sign access unit: %w", err)
		}
		auSig = sig
	}

	// Fragment PS data across multiple RTP packets
	offset := 0
	totalFragments := (len(psData) + rtpMtu - 1) / rtpMtu

	for fragmentIndex := 0; offset < len(psData); fragmentIndex++ {
		end := offset + rtpMtu
		if end > len(psData) {
			end = len(psData)
		}

		fragment := psData[offset:end]
		marker := (fragmentIndex == totalFragments-1) // Last fragment

		// Level C: each fragment XORed under the session VEK with a
		// per-packet IV (loss of one packet never desyncs the rest).
		if rp.payloadEncryptor != nil {
			fragment = rp.payloadEncryptor(fragment, ssrc, rp.seqNum)
		}

		var rtpBuf []byte
		var err error
		if marker && auSig != nil {
			rtpBuf, err = BuildRtpPacketWithExtension(fragment, marker, ssrc, rp.seqNum, timestamp, RTPExtAUSignature, auSig)
		} else {
			rtpBuf, err = BuildRtpPacket(fragment, marker, ssrc, rp.seqNum, timestamp)
		}
		if err != nil {
			return fmt.Errorf("failed to build RTP packet for fragment %d: %w", fragmentIndex, err)
		}

		// Send via UDP or TCP (with $-framing for TCP per GB/T 28181 Annex C.2)
		if rp.tcpConn != nil {
			if err := writeRTPOverTCP(rp.tcpConn, rtpBuf); err != nil {
				return fmt.Errorf("failed to send RTP packet fragment %d over TCP: %w", fragmentIndex, err)
			}
		} else {
			// WriteToUDP on an unconnected socket is non-blocking: the packet is
			// queued to the kernel send buffer and the call returns immediately
			// (only blocks if the buffer is full, which a 1400-byte PS fragment
			// at camera bitrates never saturates).
			if _, err := rp.conn.WriteToUDP(rtpBuf, rp.remoteAddr); err != nil {
				return fmt.Errorf("failed to send RTP packet fragment %d over UDP: %w", fragmentIndex, err)
			}
		}

		// Increment sequence number for next packet
		rp.seqNum++
		offset = end
	}

	if rp.metrics != nil {
		rp.metrics.PSBytesOut(int64(len(psData)))
	}

	return nil
}
