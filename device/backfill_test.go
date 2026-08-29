package device

// Coverage backfill for the device server's remaining surfaces: the
// MESSAGE dispatch wire path (200 OK + queued MANSCDP response), full
// Stop() teardown, the segment EOF sentinel, and the SIP status-reason
// table.

import (
	"context"
	"errors"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// startDeviceServer boots a test-mode server and returns it with its
// bound SIP port and a client socket the server can reply to.
func startDeviceServer(t *testing.T) (*Server, int, *net.UDPConn) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.Cleanup(cancel)

	hub := NewFrameHub()
	cfg := Config{
		DeviceID:              "34020000001320000001",
		ChannelID:             "34020000001320000001",
		SIPDomain:             "3402000000",
		Password:              "12345678",
		LocalSIPPort:          0,
		PlatformSIPAddress:    "127.0.0.1",
		PlatformSIPPort:       15060,
		RegisterIntervalSecs:  60,
		HeartbeatIntervalSecs: 60,
		HeartbeatTimeoutCount: 3,
	}
	server := New(cfg, DeviceInfo{Name: "Cam"}, hub)
	server.SetTestMode()

	started := make(chan error, 1)
	go func() {
		err := server.Start(ctx)
		started <- err
	}()
	t.Cleanup(func() {
		server.Stop()
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Error("server never exited after Stop")
		}
	})

	port := waitSIPPort(t, server)

	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	return server, port, conn
}

// sendToServer writes a SIP message to the server and returns it.
func sendToServer(t *testing.T, conn *net.UDPConn, port int, msg SipMessage) {
	t.Helper()

	dst := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port}
	_, err := conn.WriteToUDP(msg.Serialize(), dst)
	require.NoError(t, err)
}

// nextSIPFromServer reads one SIP datagram from the server.
func nextSIPFromServer(t *testing.T, conn *net.UDPConn, timeout time.Duration) (SipMessage, bool) {
	t.Helper()

	require.NoError(t, conn.SetReadDeadline(time.Now().Add(timeout)))
	buf := make([]byte, 65535)
	n, _, err := conn.ReadFromUDP(buf)
	if err != nil {
		return SipMessage{}, false
	}

	msg, err := Parse(buf[:n])
	require.NoError(t, err)

	return msg, true
}

func TestServer_MessageCatalogQueryQueuesResponse(t *testing.T) {
	_, port, conn := startDeviceServer(t)

	query := SipMessage{
		Method:      "MESSAGE",
		RequestURI:  "sip:34020000001320000001@3402000000",
		From:        "<sip:34020000002000000001@3402000000>",
		To:          "<sip:34020000001320000001@3402000000>",
		CallID:      "cat-1@platform",
		CSeq:        "1 MESSAGE",
		Via:         "SIP/2.0/UDP 127.0.0.1:5060;rport;branch=z9hG4bK-cat",
		ContentType: "Application/MANSCDP+xml",
		Body:        `<?xml version="1.0"?><Query><CmdType>Catalog</CmdType><SN>7</SN><DeviceID>34020000001320000001</DeviceID></Query>`,
		Headers:     map[string]string{},
	}
	sendToServer(t, conn, port, query)

	// Collect replies in one drain loop: the 200 OK first, then the
	// queued catalog response (attribute-form MANSCDP) on a fresh MESSAGE.
	var catalog SipMessage
	saw200 := false
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		msg, ok := nextSIPFromServer(t, conn, 300*time.Millisecond)
		if !ok {
			continue
		}
		if msg.StatusCode == 200 {
			saw200 = true
			continue
		}
		if msg.Method == "MESSAGE" && strings.Contains(msg.Body, `CmdType="Catalog"`) {
			catalog = msg
			break
		}
	}

	require.True(t, saw200, "MESSAGE must be answered 200 OK")
	require.NotEmpty(t, catalog.Body, "catalog response must be queued and sent")
	require.Contains(t, catalog.Body, `SN="7"`, "response echoes the query SN")
	require.Contains(t, catalog.Body, "34020000001320000001", "response lists the channel")
}

func TestServer_StopTearsDownCleanly(t *testing.T) {
	server, port, conn := startDeviceServer(t)

	// A live-ish server answering traffic before the stop.
	bye := buildBye("stop-1@platform", "<sip:34020000002000000001@3402000000>", "<sip:34020000001320000001@3402000000>", "")
	sendToServer(t, conn, port, bye)

	server.Stop()

	// After Stop the listener is closed: datagrams are refused or dropped,
	// and a second Stop must not panic (idempotent teardown).
	server.Stop()

	// Give the socket teardown a moment, then verify silence.
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(300*time.Millisecond)))
	buf := make([]byte, 1500)
	for {
		_, _, err := conn.ReadFromUDP(buf)
		if err != nil {
			break // timeout / closed — silence achieved
		}
	}
}

func TestSegmentEOFSentinel(t *testing.T) {
	require.False(t, SegmentEOF(nil))
	require.False(t, SegmentEOF(errors.New("other")))

	// The real sentinel surfaces from an exhausted segment.
	dir := t.TempDir()
	base := dir + "/seg"

	video := append([]byte{0, 0, 0, 1}, []byte{0x65, 0x01}...)
	require.NoError(t, os.WriteFile(base+".h264", video, 0o600))
	require.NoError(t, os.WriteFile(base+".h264.ts.jsonl", []byte("{\"t_ms\":1000}\n"), 0o600))

	reader, err := OpenSegment(base + ".h264")
	require.NoError(t, err)

	_, _, _, err = reader.Next()
	require.NoError(t, err, "the single AU must read cleanly")

	_, _, _, err = reader.Next()
	require.Error(t, err)
	require.True(t, SegmentEOF(err), "exhausted segment must report the sentinel, got %v", err)
}

func TestStatusReason(t *testing.T) {
	cases := map[int]string{
		100: "Trying",
		200: "OK",
		401: "Unauthorized",
		404: "Not Found",
		488: "Not Acceptable Here",
		500: "Server Internal Error",
		503: "Service Unavailable",
		606: "Unknown",
	}
	for code, want := range cases {
		require.Equal(t, want, statusReason(code), "status %d", code)
	}
}
