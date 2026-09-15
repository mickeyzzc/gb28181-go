package device

import (
	"context"
	"net"
	"testing"
	"time"
)

// GB/T 28181-2022 Annex I X-GB-Ver: REGISTERs carry the device's protocol
// version when configured, and the platform's version off the 200 OK is
// exposed via PlatformProtocolVersion.
func TestRegisterLifecycleXGBVer(t *testing.T) {
	platConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer platConn.Close()

	srv := newSeamTestServer(t, nil, platConn)
	srv.cfg.ProtocolVersion = "3.0"

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- srv.runRegisterLifecycleWith(ctx, srv.socketResponseSource()) }()

	// 1st REGISTER carries the version header.
	reg1, peer := readSIP(t, platConn)
	if got := reg1.Headers[XGBVerHeaderName]; got != "3.0" {
		t.Fatalf("initial REGISTER X-GB-Ver = %q, want 3.0", got)
	}

	// Digest 401 without a version (2016-era platform).
	challenge := SipMessage{
		StatusCode:      401,
		Via:             reg1.Via,
		From:            reg1.From,
		To:              reg1.To + ";tag=fake",
		CallID:          reg1.CallID,
		CSeq:            reg1.CSeq,
		WWWAuthenticate: `Digest realm="3402000000", nonce="abc", algorithm=MD5`,
		Headers:         make(map[string]string),
	}
	writeSIP(t, platConn, challenge, peer)

	// 2nd REGISTER still carries it.
	reg2, _ := readSIP(t, platConn)
	if got := reg2.Headers[XGBVerHeaderName]; got != "3.0" {
		t.Fatalf("authenticated REGISTER X-GB-Ver = %q, want 3.0", got)
	}

	// 200 OK announcing the platform's version.
	ok := SipMessage{
		StatusCode: 200,
		Via:        reg2.Via,
		From:       reg2.From,
		To:         reg2.To + ";tag=fake",
		CallID:     reg2.CallID,
		CSeq:       reg2.CSeq,
		Headers: map[string]string{
			XGBVerHeaderName: "2.0",
			"Date":           "Tue, 15 Sep 2026 07:29:00 GMT",
		},
	}
	writeSIP(t, platConn, ok, peer)

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("lifecycle: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("lifecycle did not finish")
	}
	if got := srv.PlatformProtocolVersion(); got != "2.0" {
		t.Fatalf("PlatformProtocolVersion = %q, want 2.0", got)
	}
	// §9.10.2: the same response's SIP Date is the platform clock
	// (epoch cross-checked with date -u).
	if got := srv.PlatformDateUnix(); got != 1789457340 {
		t.Fatalf("PlatformDateUnix = %d, want 1789457340", got)
	}
}

// ParseSIPDate covers all three RFC 3261 §25.1 forms; non-GMT and
// garbage are rejected.
func TestParseSIPDateForms(t *testing.T) {
	const want = int64(1789457340)
	for _, v := range []string{
		"Tue, 15 Sep 2026 07:29:00 GMT",
		"Tuesday, 15-Sep-26 07:29:00 GMT",
		"Tue Sep 15 07:29:00 2026",
	} {
		if got, ok := ParseSIPDate(v); !ok || got != want {
			t.Fatalf("ParseSIPDate(%q) = %d,%v want %d,true", v, got, ok, want)
		}
	}
	if _, ok := ParseSIPDate("not a date"); ok {
		t.Fatal("garbage must not parse")
	}
	if _, ok := ParseSIPDate("Tue, 15 Sep 2026 07:29:00 +0800"); ok {
		t.Fatal("non-GMT offsets are outside the SIP grammar")
	}
}

// Without configuration the header is omitted (byte-identical to the
// pre-2022-increment wire form) and an absent platform version stays "".
func TestRegisterLifecycleXGBVerOmittedByDefault(t *testing.T) {
	platConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer platConn.Close()

	srv := newSeamTestServer(t, nil, platConn)
	if srv.cfg.ProtocolVersion != "" {
		t.Fatal("default ProtocolVersion must be empty")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- srv.runRegisterLifecycleWith(ctx, srv.socketResponseSource()) }()

	reg1, peer := readSIP(t, platConn)
	if _, ok := reg1.Headers[XGBVerHeaderName]; ok {
		t.Fatal("X-GB-Ver must be omitted when unconfigured")
	}

	ok200 := SipMessage{
		StatusCode: 200,
		Via:        reg1.Via,
		From:       reg1.From,
		To:         reg1.To + ";tag=fake",
		CallID:     reg1.CallID,
		CSeq:       reg1.CSeq,
		Headers:    make(map[string]string),
	}
	writeSIP(t, platConn, ok200, peer)

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("lifecycle: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("lifecycle did not finish")
	}
	if got := srv.PlatformProtocolVersion(); got != "" {
		t.Fatalf("PlatformProtocolVersion = %q, want \"\"", got)
	}
}
