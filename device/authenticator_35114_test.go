//go:build gb35114

// End-to-end GB35114 A-level integration: a real device.Server driven by
// the real security35114.Authenticator against a fake platform that
// verifies the device's sign1, seals the VKEK, and checks the Note header
// of the first keepalive.

package device_test

import (
	"context"
	"crypto/ecdsa"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/mickeyzzc/gb28181-go/device"
	sec "github.com/mickeyzzc/gb28181-go/security35114"
)

const (
	itDeviceID = "34020000001320000001"
	itServerID = "34020000002000000001"
	itRandom1  = "PRAIIbutDbd5x/NKsbwwYw=="
)

var itVKEK = [16]byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15}

func itRead(t *testing.T, conn *net.UDPConn) (device.SipMessage, *net.UDPAddr) {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, 65535)
	n, peer, err := conn.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("fake platform read: %v", err)
	}
	msg, err := device.Parse(buf[:n])
	if err != nil {
		t.Fatalf("fake platform parse: %v", err)
	}
	return msg, peer
}

func itWrite(t *testing.T, conn *net.UDPConn, msg device.SipMessage, peer *net.UDPAddr) {
	t.Helper()
	if _, err := conn.WriteToUDP(msg.Serialize(), peer); err != nil {
		t.Fatalf("fake platform write: %v", err)
	}
}

func TestServerGB35114UnidirectionHandshakeAndKeepalive(t *testing.T) {
	fixtures := filepath.Join("..", "security35114", "testdata")
	devIdentity, err := sec.LoadIdentityFromFiles(
		filepath.Join(fixtures, "device_cert.pem"), filepath.Join(fixtures, "device_key.pem"))
	if err != nil {
		t.Fatalf("device identity: %v", err)
	}
	platIdentity, err := sec.LoadIdentityFromFiles(
		filepath.Join(fixtures, "platform_cert.pem"), filepath.Join(fixtures, "platform_key.pem"))
	if err != nil {
		t.Fatalf("platform identity: %v", err)
	}

	auth, err := sec.New(sec.Options{
		Device:       devIdentity,
		PlatformCert: platIdentity.Certificate,
		DeviceID:     itDeviceID,
		ServerID:     itServerID,
	})
	if err != nil {
		t.Fatalf("security35114.New: %v", err)
	}

	// Reserve a free UDP port for the device SIP socket.
	probe, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	sipPort := probe.LocalAddr().(*net.UDPAddr).Port
	probe.Close()

	platConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatalf("platform listen: %v", err)
	}
	defer platConn.Close()

	cfg := device.Config{
		DeviceID:              itDeviceID,
		ChannelID:             itDeviceID,
		SIPDomain:             "3402000000",
		Password:              "12345678",
		LocalSIPPort:          sipPort,
		PlatformSIPAddress:    "127.0.0.1",
		PlatformSIPPort:       platConn.LocalAddr().(*net.UDPAddr).Port,
		RegisterIntervalSecs:  3600,
		HeartbeatIntervalSecs: 1,
		HeartbeatTimeoutCount: 3,
		RegisterAuthenticator: auth,
	}
	hub := device.NewFrameHub()
	srv := device.New(cfg, device.DeviceInfo{}, hub)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	go func() { _ = srv.Start(ctx) }()

	// --- GB35114 A-level handshake (unidirection) ---

	reg1, peer := itRead(t, platConn)
	if got := reg1.Authorization; got == "" || reg1.Authorization[:10] != "Capability" {
		t.Fatalf("first REGISTER Authorization = %q, want Capability announcement", got)
	}

	itWrite(t, platConn, device.SipMessage{
		StatusCode:      401,
		Via:             reg1.Via,
		From:            reg1.From,
		To:              reg1.To + ";tag=plat",
		CallID:          reg1.CallID,
		CSeq:            reg1.CSeq,
		WWWAuthenticate: `Unidirection algorithm="A:SM2;H:SM3;S:SM4/OFB/PKCS5;SI:SM3-SM2", random1="` + itRandom1 + `"`,
		Headers:         map[string]string{},
	}, peer)

	reg2, _ := itRead(t, platConn)
	if got := reg2.Authorization; got == "" || got[:13] != "Unidirection " {
		t.Fatalf("second REGISTER Authorization = %q, want Unidirection", got)
	}

	// Platform side: verify sign1 with the device certificate.
	aa, err := sec.ParseAuthAuthorization(reg2.Authorization)
	if err != nil {
		t.Fatalf("ParseAuthAuthorization: %v", err)
	}
	if aa.Random1 != itRandom1 {
		t.Fatalf("echoed random1 = %q", aa.Random1)
	}
	payload := sec.SignAuthPayload(aa.Random1, aa.Random2, itServerID, sec.ConcatWireStrings)
	if err := sec.VerifyMessage(devIdentity.Certificate, payload, aa.Sign1); err != nil {
		t.Fatalf("platform could not verify device sign1: %v", err)
	}

	// Platform side: seal the VKEK to the device key and answer 200 OK.
	cryptKey, err := sec.EncryptVKEK(oneRand{}, devicePub(t, devIdentity), itVKEK[:])
	if err != nil {
		t.Fatalf("EncryptVKEK: %v", err)
	}
	itWrite(t, platConn, device.SipMessage{
		StatusCode: 200,
		Via:        reg2.Via,
		From:       reg2.From,
		To:         reg2.To,
		CallID:     reg2.CallID,
		CSeq:       reg2.CSeq,
		Headers: map[string]string{
			"SecurityInfo": `Unidirection cryptkey="` + cryptKey + `", algorithm="A:SM2;H:SM3"`,
		},
	}, peer)

	// Wait for the handshake to land (VKEK available), then the first
	// keepalive MESSAGE must carry a valid Note header.
	deadline := time.Now().Add(8 * time.Second)
	for auth.VKEK() == nil && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if auth.VKEK() == nil {
		t.Fatal("VKEK not negotiated after 200 OK")
	}

	var keepalive device.SipMessage
	for {
		m, _ := itRead(t, platConn)
		if m.Method == "MESSAGE" {
			keepalive = m
			break
		}
	}
	date := keepalive.ExtensionHeader("Date")
	note := keepalive.ExtensionHeader("Note")
	if date == "" || note == "" {
		t.Fatalf("keepalive missing Date/Note: date=%q note=%q", date, note)
	}
	if err := sec.VerifyNoteHeader(note, "MESSAGE", keepalive.From, keepalive.To,
		keepalive.CallID, date, itVKEK[:], keepalive.Body, sec.VKEKRaw); err != nil {
		t.Fatalf("keepalive Note verification: %v", err)
	}
}

// --- local helpers ---

// oneRand yields 0x01 bytes (a valid SM2 ephemeral scalar; all-zero k
// makes gmsm's encrypt loop retry forever).
type oneRand struct{}

func (oneRand) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 0x01
	}
	return len(p), nil
}

func devicePub(t *testing.T, id *sec.Identity) *ecdsa.PublicKey {
	t.Helper()
	pub, ok := id.Certificate.PublicKey.(*ecdsa.PublicKey)
	if !ok {
		t.Fatalf("device cert public key is %T, want *ecdsa.PublicKey", id.Certificate.PublicKey)
	}
	return pub
}
