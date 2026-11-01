package device

import (
	"bytes"
	"net"
	"testing"
	"time"

	"github.com/pion/rtp"
)

// The GB35114 media-security hooks on the pusher are tag-free; these
// default-build tests pin the wire contract with crypto-free fakes:
// level B rides the AU signature as an RFC 8285 two-byte-header RTP
// extension on the marker packet, level C replaces each fragment's
// payload, and ZeroSecurity wipes and detaches at teardown.

// xorCryptor flips every payload byte — encryption and decryption are the
// same operation for the XOR construction, so this models the level-C
// transform deterministically.
type xorCryptor struct{ zeroed int }

func (x *xorCryptor) Crypt(p []byte, _ uint32, _ uint16) []byte {
	out := make([]byte, len(p))
	for i, b := range p {
		out[i] = b ^ 0xA5
	}
	return out
}
func (x *xorCryptor) Zero() { x.zeroed++ }

func TestRtpPusherSecuredSendRoundTrip(t *testing.T) {
	rx, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer rx.Close()
	rxAddr := rx.LocalAddr().(*net.UDPAddr)
	conn, err := net.ListenUDP("udp", nil)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer conn.Close()

	pusher := NewRtpPusher(conn, rxAddr)
	sig := []byte("fake-au-signature")
	enc := &xorCryptor{}
	pusher.SetAUSigner(func(au []byte, ssrc, ts uint32) ([]byte, error) {
		return sig, nil
	})
	pusher.SetPayloadEncryptor(enc)

	// One fragment (< MTU): a single packet carrying both protections.
	au := bytes.Repeat([]byte{0x00, 0x00, 0x01, 0xE0}, 32)
	const ssrc = 1000000001
	if err := pusher.SendFrame(au, true, time.Now(), ssrc); err != nil {
		t.Fatalf("SendFrame: %v", err)
	}

	buf := make([]byte, 2048)
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
		t.Fatal("single-fragment AU must set the marker bit")
	}
	if pkt.Header.ExtensionProfile != rtp.ExtensionProfileTwoByte {
		t.Fatalf("extension profile = %v, want two-byte header (RFC 8285)", pkt.Header.ExtensionProfile)
	}
	if got := pkt.Header.GetExtension(RTPExtAUSignature); !bytes.Equal(got, sig) {
		t.Fatalf("AU signature extension = %x, want %x", got, sig)
	}
	for i, b := range pkt.Payload {
		if b != au[i]^0xA5 {
			t.Fatalf("payload byte %d not encrypted: %02x, want %02x", i, b, au[i]^0xA5)
		}
	}

	// Teardown wipes key material and detaches the hooks.
	pusher.ZeroSecurity()
	if enc.zeroed == 0 {
		t.Fatal("ZeroSecurity must wipe the cryptor's key material")
	}
	if err := pusher.SendFrame(au, true, time.Now(), ssrc); err != nil {
		t.Fatalf("post-Zero SendFrame: %v", err)
	}
	rx.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, _, err = rx.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("read after Zero: %v", err)
	}
	var plain rtp.Packet
	if err := plain.Unmarshal(buf[:n]); err != nil {
		t.Fatalf("unmarshal after Zero: %v", err)
	}
	if got := plain.Header.GetExtension(RTPExtAUSignature); got != nil {
		t.Fatal("post-Zero packet must carry no signature extension")
	}
	if !bytes.Equal(plain.Payload, au) {
		t.Fatal("post-Zero payload must be plaintext")
	}
}

// BuildRtpPacketWithExtension round-trips the RFC 8285 two-byte-header
// form the level-B signature rides on.
func TestBuildRtpPacketWithExtension(t *testing.T) {
	ext := bytes.Repeat([]byte{0xEE}, 40) // > 16 bytes: forces two-byte profile
	pkt, err := BuildRtpPacketWithExtension([]byte("payload"), true, 42, 7, 9000, RTPExtAUSignature, ext)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	var p rtp.Packet
	if err := p.Unmarshal(pkt); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got := p.Header.GetExtension(RTPExtAUSignature); !bytes.Equal(got, ext) {
		t.Fatalf("extension round-trip = %x", got)
	}
	if !p.Marker || p.Header.SequenceNumber != 7 || p.Header.Timestamp != 9000 || p.Header.SSRC != 42 {
		t.Fatalf("header fields drifted: %+v", p.Header)
	}
}
