package sip

import (
	"bufio"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mickeyzzc/gb28181-go/manscdp"
	"github.com/stretchr/testify/require"
)

// tcpDevice is a minimal GB28181 device speaking SIP over a TCP connection:
// it REGISTERs with Digest auth, then answers requests the platform sends on
// that same connection.
type tcpDevice struct {
	t    *testing.T
	conn net.Conn
	rd   *bufio.Reader
}

func dialTCPDevice(t *testing.T, addr string) *tcpDevice {
	t.Helper()
	conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
	if err != nil {
		t.Fatalf("dial tcp %s: %v", addr, err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return &tcpDevice{t: t, conn: conn, rd: bufio.NewReader(conn)}
}

// readMessage reads one SIP message using Content-Length framing.
func (d *tcpDevice) readMessage(timeout time.Duration) string {
	d.t.Helper()
	_ = d.conn.SetReadDeadline(time.Now().Add(timeout))
	var sb strings.Builder
	for {
		line, err := d.rd.ReadString('\n')
		if err != nil {
			return ""
		}
		sb.WriteString(line)
		if strings.TrimSpace(line) == "" {
			break
		}
	}
	cl := 0
	for _, h := range strings.Split(sb.String(), "\r\n") {
		if strings.HasPrefix(strings.ToLower(h), "content-length:") {
			v, err := strconv.Atoi(strings.TrimSpace(h[len("content-length:"):]))
			if err == nil {
				cl = v
			}
		}
	}
	buf := make([]byte, cl)
	read := 0
	for read < cl {
		n, err := d.rd.Read(buf[read:])
		if err != nil {
			break
		}
		read += n
	}
	sb.Write(buf[:read])
	return sb.String()
}

func (d *tcpDevice) send(s string) {
	d.t.Helper()
	_ = d.conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
	if _, err := d.conn.Write([]byte(s)); err != nil {
		d.t.Fatalf("tcp device write: %v", err)
	}
}

func (d *tcpDevice) register(deviceID string, cfg Config) {
	d.t.Helper()
	reg := fmt.Sprintf(
		"REGISTER sip:%s@%s SIP/2.0\r\nVia: SIP/2.0/TCP 127.0.0.1:55060;branch=z9hG4bKtcpreg1\r\n"+
			"From: <sip:%s@%s>;tag=tcpdev1\r\nTo: <sip:%s@%s>\r\nCall-ID: tcp-reg-1@127.0.0.1\r\n"+
			"CSeq: 1 REGISTER\r\nContact: <sip:%s@127.0.0.1:55060>\r\nMax-Forwards: 70\r\n"+
			"Expires: 3600\r\nContent-Length: 0\r\n\r\n",
		deviceID, cfg.Realm, deviceID, cfg.Realm, deviceID, cfg.Realm, deviceID)
	d.send(reg)
	res := d.readMessage(3 * time.Second)
	require.Contains(d.t, res, "401", "first REGISTER must be challenged")
	nonce, realm := "", cfg.Realm
	for _, seg := range strings.Split(res, ",") {
		seg = strings.TrimSpace(seg)
		if v, ok := strings.CutPrefix(seg, "nonce="); ok {
			nonce = strings.Trim(v, "\"\r\n")
		}
		if v, ok := strings.CutPrefix(seg, "realm="); ok {
			realm = strings.Trim(v, "\"\r\n")
		}
	}
	require.NotEmpty(d.t, nonce, "challenge nonce")
	ha1 := md5hex(deviceID + ":" + realm + ":" + cfg.Password)
	ha2 := md5hex("REGISTER:" + "sip:" + deviceID + "@" + cfg.Realm)
	resp := md5hex(ha1 + ":" + nonce + ":" + ha2)
	reg2 := fmt.Sprintf(
		"REGISTER sip:%s@%s SIP/2.0\r\nVia: SIP/2.0/TCP 127.0.0.1:55060;branch=z9hG4bKtcpreg2\r\n"+
			"From: <sip:%s@%s>;tag=tcpdev1\r\nTo: <sip:%s@%s>\r\nCall-ID: tcp-reg-1@127.0.0.1\r\n"+
			"CSeq: 2 REGISTER\r\nContact: <sip:%s@127.0.0.1:55060>\r\nMax-Forwards: 70\r\n"+
			"Expires: 3600\r\nAuthorization: Digest username=\"%s\", realm=\"%s\", nonce=\"%s\", uri=\"sip:%s@%s\", response=\"%s\", algorithm=MD5\r\n"+
			"Content-Length: 0\r\n\r\n",
		deviceID, cfg.Realm, deviceID, cfg.Realm, deviceID, cfg.Realm, deviceID,
		deviceID, realm, nonce, deviceID, cfg.Realm, resp)
	d.send(reg2)
	res2 := d.readMessage(3 * time.Second)
	require.Contains(d.t, res2, "200", "authenticated REGISTER must succeed: %s", firstLine(res2))
}

func md5hex(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

func firstLine(s string) string {
	if i := strings.Index(s, "\r\n"); i >= 0 {
		return s[:i]
	}
	return s
}

func headerLine(msg, name string) string {
	for _, l := range strings.Split(msg, "\r\n") {
		if strings.HasPrefix(strings.ToLower(l), strings.ToLower(name)+":") {
			return l[len(name)+2:]
		}
	}
	return ""
}

// TestTCPRegisteredDeviceReceivesInviteOverTCP pins the routing fix: a device
// that REGISTERed over TCP must receive platform-initiated INVITEs on its TCP
// connection. Before the fix the INVITE was sent over UDP to the device's TCP
// source port and died unanswered, looping the media session (MiBeeNvr #1058).
func TestTCPRegisteredDeviceReceivesInviteOverTCP(t *testing.T) {
	cfg := testConfig(t)
	cfg.SIPTransport = "tcp"
	// testConfig probes with UDP; Windows excluded-port ranges can forbid
	// the TCP bind on that port. Probe with a real TCP listener instead.
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := probe.Addr().(*net.TCPAddr).Port
	_ = probe.Close()
	cfg.SIPListen = fmt.Sprintf("127.0.0.1:%d", port)
	srv, dm := startTestServer(t, cfg)

	var auCount atomic.Int64
	enrol := &fakeEnroller{naluWriter: func(au [][]byte, ptsTicks int64, isIDR bool) {
		auCount.Add(1)
	}}
	require.NoError(t, enrol.EnsureGB28181Camera(testDeviceID, fakeChannelID, "TCP Cam", "127.0.0.1"))
	srv.SetCameraEnroller(enrol)

	dev := dialTCPDevice(t, cfg.SIPListen)
	dev.register(testDeviceID, cfg)

	// The REGISTER transport must be captured on the device record.
	require.Eventually(t, func() bool {
		d, ok := dm.Device(testDeviceID)
		if !ok {
			return false
		}
		d.Mu.RLock()
		defer d.Mu.RUnlock()
		return d.Transport == "tcp"
	}, 5*time.Second, 20*time.Millisecond, "REGISTER transport must be recorded")

	// Catalog over the same TCP connection triggers the channel auto-INVITE.
	catalog, err := manscdp.Encode(manscdp.Catalog{
		CmdType: manscdp.CmdCatalog, SN: 1, DeviceID: testDeviceID, SumNum: 1,
		Item: []manscdp.Item{{DeviceID: fakeChannelID, Name: "TCP Cam", Parental: 0}},
	})
	require.NoError(t, err)
	dev.send(fmt.Sprintf(
		"MESSAGE sip:%s@%s SIP/2.0\r\nVia: SIP/2.0/TCP 127.0.0.1:55060;branch=z9hG4bKcat1\r\n"+
			"From: <sip:%s@%s>;tag=tcpdev1\r\nTo: <sip:%s@%s>\r\nCall-ID: tcp-cat-1@127.0.0.1\r\n"+
			"CSeq: 1 MESSAGE\r\nMax-Forwards: 70\r\nContent-Type: Application/MANSCDP+xml\r\n"+
			"Content-Length: %d\r\n\r\n%s",
		testDeviceID, cfg.Realm, testDeviceID, cfg.Realm, cfg.ServerID, cfg.Realm,
		len(string(catalog)), string(catalog)))

	// Answer EVERY INVITE that arrives on the TCP connection with its own SDP
	// echoed back (the device-self channel auto-INVITEs right after REGISTER,
	// the catalog channel after the catalog). The regression assertion: an
	// INVITE must arrive on this TCP connection at all — it used to leave via
	// UDP to the TCP source port and never reach the device.
	answer := func(inv string) {
		body := inv[strings.Index(inv, "v=0"):]
		if i := strings.Index(body, "\r\n\r\n"); i > 0 {
			body = body[:i]
		}
		resp := fmt.Sprintf(
			"SIP/2.0 200 OK\r\nVia: %s\r\nFrom: %s\r\nTo: %s;tag=tcpdev-to-1\r\n"+
				"Call-ID: %s\r\nCSeq: 1 INVITE\r\nContact: <sip:%s@127.0.0.1:55060>\r\n"+
				"Content-Type: application/sdp\r\nContent-Length: %d\r\n\r\n%s",
			strings.TrimSpace(headerLine(inv, "Via")), strings.TrimSpace(headerLine(inv, "From")),
			strings.TrimSpace(headerLine(inv, "To")), strings.TrimSpace(headerLine(inv, "Call-ID")),
			testDeviceID, len(body), body)
		dev.send(resp)
	}

	var sawTCPInvite atomic.Bool
	var targetMediaPort atomic.Int32
	require.Eventually(t, func() bool {
		for {
			msg := dev.readMessage(150 * time.Millisecond)
			if msg == "" {
				break
			}
			if !strings.HasPrefix(msg, "INVITE ") {
				continue
			}
			sawTCPInvite.Store(true)
			require.Contains(t, msg, "transport=tcp", "recipient URI must carry ;transport=tcp")
			answer(msg)
			if strings.Contains(msg, fakeChannelID) {
				body := msg[strings.Index(msg, "v=0"):]
				if i := strings.Index(body, "\r\n\r\n"); i > 0 {
					body = body[:i]
				}
				targetMediaPort.Store(int32(sdpMediaPort(t, body)))
			}
		}
		srv.mu.Lock()
		dlg := srv.dialogs[fakeChannelID]
		srv.mu.Unlock()
		return dlg != nil && targetMediaPort.Load() != 0
	}, 12*time.Second, 50*time.Millisecond, "INVITEs must arrive and be answered over TCP; catalog dialog must confirm")
	require.True(t, sawTCPInvite.Load(), "INVITE must arrive over the device's TCP connection")

	// UDP media to the offered port keeps the session alive — the signaling
	// transport says nothing about the media transport.
	media, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	require.NoError(t, err)
	t.Cleanup(func() { _ = media.Close() })
	payload := []byte{
		0x80, 0x60, 0x00, 0x01, // RTP v2, PS payload type 96
		0x00, 0x00, 0x00, 0x64, // SSRC 0x64
	}
	require.Eventually(t, func() bool {
		_, _ = media.WriteToUDP(payload, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: int(targetMediaPort.Load())})
		return auCount.Load() >= 0
	}, 3*time.Second, 100*time.Millisecond, "session stays up with media")
}
