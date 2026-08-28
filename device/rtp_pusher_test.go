package device

import (
	"net"
	"testing"
	"time"

	"github.com/pion/rtp"
)

// TestRtpPusher_UsesProvidedSSRC verifies that BuildRtpPacket uses the provided SSRC.
func TestRtpPusher_UsesProvidedSSRC(t *testing.T) {
	providedSSRC := uint32(12345)
	payload := []byte{0x00, 0x00, 0x01, 0xBA} // PS pack header start code

	packetBytes, err := BuildRtpPacket(payload, false, providedSSRC, 42, 1234567)
	if err != nil {
		t.Fatalf("BuildRtpPacket failed: %v", err)
	}

	// Parse the packet using pion/rtp to verify SSRC
	packet := &rtp.Packet{}
	if err := packet.Unmarshal(packetBytes); err != nil {
		t.Fatalf("Failed to unmarshal RTP packet: %v", err)
	}

	if packet.SSRC != providedSSRC {
		t.Errorf("Expected SSRC %d, got %d", providedSSRC, packet.SSRC)
	}
}

// TestRtpPusher_Fragmentation_MtuBoundary verifies correct fragmentation at MTU 1400.
func TestRtpPusher_Fragmentation_MtuBoundary(t *testing.T) {
	// Create UDP pair for loopback testing
	listenerAddr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to resolve listener address: %v", err)
	}
	listener, err := net.ListenUDP("udp", listenerAddr)
	if err != nil {
		t.Fatalf("Failed to create listener: %v", err)
	}
	defer listener.Close()

	// Get the actual listening address
	actualAddr := listener.LocalAddr().(*net.UDPAddr)

	conn, err := net.ListenUDP("udp", nil)
	if err != nil {
		t.Fatalf("Failed to create sender: %v", err)
	}
	defer conn.Close()

	pusher := NewRtpPusher(conn, actualAddr)

	// Create 3000 bytes of PS data
	psData := make([]byte, 3000)
	for i := range psData {
		psData[i] = byte(i & 0xFF)
	}

	pts := time.Unix(1234567890, 0)
	ssrc := uint32(98765)

	// Send the frame
	done := make(chan error, 1)
	go func() {
		done <- pusher.SendFrame(psData, true, pts, ssrc)
	}()

	// Receive and count packets
	packetCount := 0
	receivedSizes := make([]int, 0, 5)
	buf := make([]byte, 1600) // Large enough for max packet

	for {
		listener.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
		n, _, err := listener.ReadFromUDP(buf)
		if err != nil {
			break // Timeout or error - no more packets
		}
		packetCount++
		receivedSizes = append(receivedSizes, n)
	}

	// Wait for send to complete
	if err := <-done; err != nil {
		t.Fatalf("SendFrame failed: %v", err)
	}

	// Verify fragmentation: 3000 bytes should fragment into 3 packets (1400 + 1400 + 200)
	if packetCount != 3 {
		t.Errorf("Expected 3 packets, got %d", packetCount)
	}

	// Verify packet sizes (header is 12 bytes, so payload sizes are n-12)
	if len(receivedSizes) != 3 {
		t.Fatalf("Expected 3 packet sizes, got %d", len(receivedSizes))
	}

	expectedPayloadSizes := []int{1400, 1400, 200}
	for i, size := range receivedSizes {
		payloadSize := size - 12 // Subtract RTP header
		if payloadSize != expectedPayloadSizes[i] {
			t.Errorf("Packet %d: expected payload size %d, got %d (packet size %d)",
				i, expectedPayloadSizes[i], payloadSize, size)
		}
	}
}

// TestRtpPusher_SequenceIncrements verifies that RTP sequence numbers increment correctly.
func TestRtpPusher_SequenceIncrements(t *testing.T) {
	// Create UDP pair for loopback testing
	listenerAddr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to resolve listener address: %v", err)
	}
	listener, err := net.ListenUDP("udp", listenerAddr)
	if err != nil {
		t.Fatalf("Failed to create listener: %v", err)
	}
	defer listener.Close()

	actualAddr := listener.LocalAddr().(*net.UDPAddr)
	conn, err := net.ListenUDP("udp", nil)
	if err != nil {
		t.Fatalf("Failed to create sender: %v", err)
	}
	defer conn.Close()

	pusher := NewRtpPusher(conn, actualAddr)

	// Create PS data that will fragment into 2 packets
	psData := make([]byte, 1500) // 1400 + 100 = 2 packets
	for i := range psData {
		psData[i] = byte(i & 0xFF)
	}

	pts := time.Unix(1234567890, 0)
	ssrc := uint32(11111)

	// Send the frame
	done := make(chan error, 1)
	go func() {
		done <- pusher.SendFrame(psData, true, pts, ssrc)
	}()

	// Receive and parse packets to check sequence numbers
	packet := &rtp.Packet{}
	buf := make([]byte, 1600)
	seqNums := make([]uint16, 0, 2)

	for {
		listener.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
		n, _, err := listener.ReadFromUDP(buf)
		if err != nil {
			break
		}

		if err := packet.Unmarshal(buf[:n]); err != nil {
			t.Fatalf("Failed to unmarshal packet: %v", err)
		}
		seqNums = append(seqNums, packet.SequenceNumber)
	}

	if err := <-done; err != nil {
		t.Fatalf("SendFrame failed: %v", err)
	}

	// Verify we received 2 packets
	if len(seqNums) != 2 {
		t.Fatalf("Expected 2 packets, got %d", len(seqNums))
	}

	// Verify sequence numbers increment by 1
	if seqNums[1] != seqNums[0]+1 {
		t.Errorf("Expected sequence numbers to increment by 1: got %d -> %d",
			seqNums[0], seqNums[1])
	}
}

// TestSendFrame_OverLoopback performs a full loopback test.
// Creates loopback UDP, sends frame via pusher, receives on listener,
// parses RTP and verifies PT=96, payload is PS data, SSRC matches provided value.
func TestSendFrame_OverLoopback(t *testing.T) {
	// Create UDP pair for loopback testing
	listenerAddr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to resolve listener address: %v", err)
	}
	listener, err := net.ListenUDP("udp", listenerAddr)
	if err != nil {
		t.Fatalf("Failed to create listener: %v", err)
	}
	defer listener.Close()

	actualAddr := listener.LocalAddr().(*net.UDPAddr)
	conn, err := net.ListenUDP("udp", nil)
	if err != nil {
		t.Fatalf("Failed to create sender: %v", err)
	}
	defer conn.Close()

	pusher := NewRtpPusher(conn, actualAddr)

	// Create a small PS packet (no fragmentation)
	psData := []byte{0x00, 0x00, 0x01, 0xBA, 0x44, 0x00, 0x04, 0x00, 0x00, 0x04, 0x00, 0x01, 0x01, 0xF8}

	pts := time.Unix(1234567890, 0)
	expectedSSRC := uint32(22222)

	// Send the frame
	done := make(chan error, 1)
	go func() {
		done <- pusher.SendFrame(psData, true, pts, expectedSSRC)
	}()

	// Receive and parse the packet
	packet := &rtp.Packet{}
	buf := make([]byte, 1600)

	listener.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	n, _, err := listener.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("Failed to receive packet: %v", err)
	}

	if err := packet.Unmarshal(buf[:n]); err != nil {
		t.Fatalf("Failed to unmarshal packet: %v", err)
	}

	// Wait for send to complete
	if err := <-done; err != nil {
		t.Fatalf("SendFrame failed: %v", err)
	}

	// Verify PT=96 (PS over RTP per GB28181)
	if packet.PayloadType != 96 {
		t.Errorf("Expected payload type 96, got %d", packet.PayloadType)
	}

	// Verify SSRC matches provided value
	if packet.SSRC != expectedSSRC {
		t.Errorf("Expected SSRC %d, got %d", expectedSSRC, packet.SSRC)
	}

	// Verify payload contains PS data
	if len(packet.Payload) != len(psData) {
		t.Errorf("Expected payload length %d, got %d", len(psData), len(packet.Payload))
	}

	for i, b := range psData {
		if i < len(packet.Payload) && packet.Payload[i] != b {
			t.Errorf("Payload mismatch at byte %d: expected %02x, got %02x",
				i, b, packet.Payload[i])
			break
		}
	}

	// Verify marker bit is set (single packet is last fragment)
	if !packet.Marker {
		t.Errorf("Expected marker bit to be set on last fragment")
	}
}

// TestBuildRtpPacket_InvalidPayload verifies error handling in BuildRtpPacket.
func TestBuildRtpPacket_AllFields(t *testing.T) {
	payload := []byte{0x01, 0x02, 0x03, 0x04}
	ssrc := uint32(12345)
	seqNum := uint16(42)
	timestamp := uint32(1234567)
	marker := true

	packetBytes, err := BuildRtpPacket(payload, marker, ssrc, seqNum, timestamp)
	if err != nil {
		t.Fatalf("BuildRtpPacket failed: %v", err)
	}

	// Parse and verify all fields
	packet := &rtp.Packet{}
	if err := packet.Unmarshal(packetBytes); err != nil {
		t.Fatalf("Failed to unmarshal RTP packet: %v", err)
	}

	if packet.Version != 2 {
		t.Errorf("Expected RTP version 2, got %d", packet.Version)
	}
	if packet.PayloadType != 96 {
		t.Errorf("Expected payload type 96, got %d", packet.PayloadType)
	}
	if packet.Marker != marker {
		t.Errorf("Expected marker %v, got %v", marker, packet.Marker)
	}
	if packet.SequenceNumber != seqNum {
		t.Errorf("Expected sequence number %d, got %d", seqNum, packet.SequenceNumber)
	}
	if packet.Timestamp != timestamp {
		t.Errorf("Expected timestamp %d, got %d", timestamp, packet.Timestamp)
	}
	if packet.SSRC != ssrc {
		t.Errorf("Expected SSRC %d, got %d", ssrc, packet.SSRC)
	}
}
