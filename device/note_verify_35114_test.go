//go:build gb35114

// End-to-end device-side downstream Note verification (issue #52): after
// a real A-level handshake, a fake platform signs its downstream request
// with the negotiated VKEK — the device serves it; the same request with
// a tampered body draws a 403 under the default reject policy.

package device_test

import (
	"context"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/mickeyzzc/gb28181-go/device"
	sec "github.com/mickeyzzc/gb28181-go/security35114"
)

// start35114NoteServer performs the full GB35114 Unidirection handshake
// against a fake platform and returns the platform socket, the device's
// address, and the device authenticator (VKEK established).
func start35114NoteServer(t *testing.T) (*net.UDPConn, *net.UDPAddr, *sec.Authenticator) {
	t.Helper()

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
		Rand:         oneRand{},
	})
	if err != nil {
		t.Fatalf("security35114.New: %v", err)
	}

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
	t.Cleanup(func() { _ = platConn.Close() })

	cfg := device.Config{
		DeviceID:              itDeviceID,
		ChannelID:             itDeviceID,
		SIPDomain:             "3402000000",
		Password:              "12345678",
		LocalSIPPort:          sipPort,
		PlatformSIPAddress:    "127.0.0.1",
		PlatformSIPPort:       platConn.LocalAddr().(*net.UDPAddr).Port,
		RegisterIntervalSecs:  3600,
		HeartbeatIntervalSecs: 3600,
		HeartbeatTimeoutCount: 3,
		RegisterAuthenticator: auth,
	}
	srv := device.New(cfg, device.DeviceInfo{}, device.NewFrameHub())

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.Cleanup(func() { cancel(); srv.Stop() })
	go func() { _ = srv.Start(ctx) }()

	// --- Unidirection handshake ---
	reg1, peer := itRead(t, platConn)
	if reg1.Authorization == "" || reg1.Authorization[:10] != "Capability" {
		t.Fatalf("first REGISTER Authorization = %q, want Capability", reg1.Authorization)
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

	deadline := time.Now().Add(8 * time.Second)
	for auth.VKEK() == nil && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if auth.VKEK() == nil {
		t.Fatal("VKEK not negotiated after 200 OK")
	}
	return platConn, peer, auth
}

// signedOptions builds an OPTIONS request whose Note covers body (pass ""
// to sign the empty body; a non-empty body with a signature over ""
// simulates tampering).
func signedOptions(callID, from, to, body string) device.SipMessage {
	date := sec.FormatDate(time.Now())
	note := sec.BuildNoteHeader("OPTIONS", from, to, callID, date, itVKEK[:], body, sec.VKEKRaw)
	headers := map[string]string{"Note": note, "Date": date}
	if body != "" {
		headers["Content-Type"] = "application/xml"
	}
	return device.SipMessage{
		Method:     "OPTIONS",
		RequestURI: "sip:" + to,
		From:       from,
		To:         to,
		CallID:     callID,
		CSeq:       "1 OPTIONS",
		Via:        "SIP/2.0/UDP 127.0.0.1:5060;branch=z9hG4bKnv" + callID,
		Body:       body,
		UserAgent:  "fakeplatform",
		Headers:    headers,
	}
}

func TestServerGB35114VerifiesDownstreamNote(t *testing.T) {
	platConn, peer, _ := start35114NoteServer(t)

	from := "<sip:34020000002000000001@3402000000>;tag=plat1"
	to := "<sip:34020000001320000001@3402000000>"

	// A properly signed platform request is served.
	itWrite(t, platConn, signedOptions("nv-good", from, to, ""), peer)
	resp, _ := itRead(t, platConn)
	if resp.StatusCode != 200 {
		t.Fatalf("signed OPTIONS: status = %d, want 200", resp.StatusCode)
	}

	// The same Note with a tampered body draws 403 under the default
	// reject policy.
	tampered := signedOptions("nv-bad", from, to, "")
	tampered.Body = "<tampered/>"
	itWrite(t, platConn, tampered, peer)
	resp, _ = itRead(t, platConn)
	if resp.StatusCode != 403 {
		t.Fatalf("tampered OPTIONS: status = %d, want 403", resp.StatusCode)
	}
}
