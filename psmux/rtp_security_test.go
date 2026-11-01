package psmux

import (
	"bytes"
	"net"
	"testing"
	"time"

	"github.com/pion/rtp"
)

// SetSecurity arms the GB35114 hooks on the packetizer (tag-free seams —
// crypto-free fakes here): the AU signature rides the marker fragment as
// an RFC 8285 two-byte-header extension and every fragment's payload is
// transformed. Default (unarmed) sends stay byte-identical to the legacy
// path — pinned separately by the round-trip suite.

func TestRTPPacketizerSecuredSend(t *testing.T) {
	rx, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer rx.Close()
	tx, err := net.ListenUDP("udp", nil)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer tx.Close()
	dst := rx.LocalAddr().(*net.UDPAddr)

	sig := []byte("packetizer-fake-sig")
	pktz := NewRTPPacketizer(tx, dst, 77, 100)
	pktz.SetSecurity(
		func(p []byte, _ uint32, _ uint16) []byte {
			out := make([]byte, len(p))
			for i, b := range p {
				out[i] = b ^ 0x5A
			}
			return out
		},
		func(au []byte, ssrc uint32, ts uint32) ([]byte, error) {
			return sig, nil
		},
	)

	ps := bytes.Repeat([]byte{0x00, 0x00, 0x01, 0xBA}, 16) // < RTPMTU: one fragment
	if err := pktz.Send(ps, 90000); err != nil {
		t.Fatalf("Send: %v", err)
	}

	buf := make([]byte, 4096)
	rx.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, _, err := rx.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var pkt rtp.Packet
	if err := pkt.Unmarshal(buf[:n]); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !pkt.Marker {
		t.Fatal("single-fragment burst must set the marker")
	}
	if got := pkt.Header.GetExtension(1); !bytes.Equal(got, sig) {
		t.Fatalf("signature extension = %x", got)
	}
	for i, b := range pkt.Payload {
		if b != ps[i]^0x5A {
			t.Fatalf("payload byte %d not transformed", i)
		}
	}
}
