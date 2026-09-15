package device_test

// Wire-level tests for the SUBSCRIBE/NOTIFY framework (issue #80): a
// real device Server over a real UDP socket — SUBSCRIBE in, 200 with
// echoed Expires out, NOTIFYs on the subscription dialog. The golden
// bodies are byte-compatible with the Rust twin (gb28181-rs #66).

import (
	"context"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mickeyzzc/gb28181-go/device"
	"github.com/mickeyzzc/gb28181-go/manscdp"
)

func startSubTestServer(t *testing.T) (*device.Server, *net.UDPConn, *net.UDPAddr) {
	t.Helper()

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
		DeviceID:              "34020000001320000001",
		ChannelID:             "34020000001320000001",
		SIPDomain:             "3402000000",
		Password:              "12345678",
		LocalSIPPort:          sipPort,
		PlatformSIPAddress:    "127.0.0.1",
		PlatformSIPPort:       platConn.LocalAddr().(*net.UDPAddr).Port,
		RegisterIntervalSecs:  3600,
		HeartbeatIntervalSecs: 3600,
		HeartbeatTimeoutCount: 3,
		RegisterAuthenticator: snapStubAuth{},
	}
	srv := device.New(cfg, device.DeviceInfo{}, device.NewFrameHub())

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.Cleanup(cancel)
	go func() { _ = srv.Start(ctx) }()

	reg, peer := readSnapMsg(t, platConn)
	if reg.Method != "REGISTER" {
		t.Fatalf("first message from device = %q, want REGISTER", reg.Method)
	}
	writeSnapMsg(t, platConn, device.SipMessage{
		StatusCode: 200,
		Via:        reg.Via,
		From:       reg.From,
		To:         reg.To,
		CallID:     reg.CallID,
		CSeq:       reg.CSeq,
		Headers:    map[string]string{},
	}, peer)

	return srv, platConn, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: sipPort}
}

func subscribeMessage(callID, event string, expires int) device.SipMessage {
	return device.SipMessage{
		Method:     "SUBSCRIBE",
		RequestURI: "sip:34020000001320000001@3402000000",
		From:       "<sip:34020000002000000001@3402000000>;tag=sub" + callID,
		To:         "<sip:34020000001320000001@3402000000>",
		CallID:     callID,
		CSeq:       "1 SUBSCRIBE",
		Via:        "SIP/2.0/UDP 127.0.0.1:5060;branch=z9hG4bKsub" + callID,
		Expires:    strconv.Itoa(expires),
		Headers:    map[string]string{"Event": event},
	}
}

func readNotify(t *testing.T, conn *net.UDPConn) device.SipMessage {
	t.Helper()
	msg, _ := readSnapMsg(t, conn)
	return msg
}

func TestSubscribeBooksAndEchoesExpires(t *testing.T) {
	srv, platConn, devAddr := startSubTestServer(t)

	writeSnapMsg(t, platConn, subscribeMessage("sub-1", "Alarm", 1800), devAddr)
	ok := readNotify(t, platConn)
	if ok.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", ok.StatusCode)
	}
	if ok.Expires != "1800" {
		t.Fatalf("Expires echo = %q, want 1800", ok.Expires)
	}
	if !srv.Notifier().Subscribed(device.EventAlarm) {
		t.Fatal("Alarm subscription not booked")
	}
	if srv.Notifier().Subscribed(device.EventCatalog) {
		t.Fatal("Catalog must not be booked")
	}

	// Unsupported subject: answered, not booked.
	writeSnapMsg(t, platConn, subscribeMessage("sub-2", "Presence", 1800), devAddr)
	ok = readNotify(t, platConn)
	if ok.StatusCode != 200 {
		t.Fatalf("unsupported subject status = %d, want 200", ok.StatusCode)
	}
	if srv.Notifier().Subscribed(device.EventMobilePosition) {
		t.Fatal("MobilePosition must not be booked by a Presence subscribe")
	}
}

func TestAlarmNotifyGolden(t *testing.T) {
	srv, platConn, devAddr := startSubTestServer(t)

	// No subscription yet: safe no-op.
	if srv.Notifier().SendAlarm("2", "5", "2026-09-15T12:00:00", "1", "motion") {
		t.Fatal("SendAlarm must no-op without a subscription")
	}

	writeSnapMsg(t, platConn, subscribeMessage("sub-a", "Alarm", 1800), devAddr)
	readNotify(t, platConn) // 200 OK

	if !srv.Notifier().SendAlarm("2", "5", "2026-09-15T12:00:00", "1", "motion") {
		t.Fatal("SendAlarm failed with a live subscription")
	}
	notify := readNotify(t, platConn)
	if notify.Method != "NOTIFY" {
		t.Fatalf("method = %q, want NOTIFY", notify.Method)
	}
	if notify.Headers["Event"] != "Alarm" {
		t.Fatalf("Event header = %q", notify.Headers["Event"])
	}
	if !strings.HasPrefix(notify.Headers["Subscription-State"], "active") {
		t.Fatalf("Subscription-State = %q", notify.Headers["Subscription-State"])
	}
	if !strings.Contains(notify.CSeq, "NOTIFY") {
		t.Fatalf("CSeq = %q", notify.CSeq)
	}
	// §9.5.2 body — field order byte-compatible with the Rust twin; this
	// repo's encoder prepends the XML declaration (its established
	// convention — both twins' parsers accept either form). The SN is 2:
	// the no-op send above consumed SN 1 (SNs are monotonic, not dense).
	want := "<?xml version=\"1.0\" encoding=\"GB2312\"?>\n" +
		"<Notify><CmdType>Alarm</CmdType><SN>2</SN><DeviceID>34020000001320000001</DeviceID>" +
		"<AlarmPriority>2</AlarmPriority><AlarmMethod>5</AlarmMethod>" +
		"<AlarmTime>2026-09-15T12:00:00</AlarmTime><AlarmDescription>motion</AlarmDescription>" +
		"<AlarmType>1</AlarmType></Notify>"
	if notify.Body != want {
		t.Fatalf("alarm body:\n got %s\nwant %s", notify.Body, want)
	}
}

func TestMobilePositionNotifyGolden(t *testing.T) {
	srv, platConn, devAddr := startSubTestServer(t)
	writeSnapMsg(t, platConn, subscribeMessage("sub-m", "MobilePosition", 1800), devAddr)
	readNotify(t, platConn)

	if !srv.Notifier().SendMobilePosition(&device.PositionReport{
		Time: "2026-09-15T12:00:01", Longitude: "116.40", Latitude: "39.90",
		Speed: "0.0", Direction: "0.0", Altitude: "50.0",
	}) {
		t.Fatal("SendMobilePosition failed")
	}
	notify := readNotify(t, platConn)
	if notify.Headers["Event"] != "MobilePosition" {
		t.Fatalf("Event header = %q", notify.Headers["Event"])
	}
	want := "<?xml version=\"1.0\" encoding=\"GB2312\"?>\n" +
		"<Notify><CmdType>MobilePosition</CmdType><SN>1</SN><DeviceID>34020000001320000001</DeviceID>" +
		"<Time>2026-09-15T12:00:01</Time><Longitude>116.40</Longitude><Latitude>39.90</Latitude>" +
		"<Speed>0.0</Speed><Direction>0.0</Direction><Altitude>50.0</Altitude></Notify>"
	if notify.Body != want {
		t.Fatalf("position body:\n got %s\nwant %s", notify.Body, want)
	}
}

func TestCatalogChangeNotify(t *testing.T) {
	srv, platConn, devAddr := startSubTestServer(t)
	writeSnapMsg(t, platConn, subscribeMessage("sub-c", "Catalog", 1800), devAddr)
	readNotify(t, platConn)

	if !srv.Notifier().SendCatalogChange([]manscdp.Item{{
		DeviceID: "34020000001320000001", Name: "cam", Status: "ON",
	}}) {
		t.Fatal("SendCatalogChange failed")
	}
	notify := readNotify(t, platConn)
	if notify.Headers["Event"] != "Catalog" {
		t.Fatalf("Event header = %q", notify.Headers["Event"])
	}
	if !strings.Contains(notify.Body, "<SumNum>1</SumNum>") ||
		!strings.Contains(notify.Body, "<DeviceID>34020000001320000001</DeviceID>") {
		t.Fatalf("catalog body: %s", notify.Body)
	}
}

func TestSubscriptionExpires(t *testing.T) {
	srv, platConn, devAddr := startSubTestServer(t)
	writeSnapMsg(t, platConn, subscribeMessage("sub-e", "Alarm", 1), devAddr)
	readNotify(t, platConn)
	if !srv.Notifier().Subscribed(device.EventAlarm) {
		t.Fatal("subscription should be live")
	}
	time.Sleep(1100 * time.Millisecond)
	if srv.Notifier().Subscribed(device.EventAlarm) {
		t.Fatal("subscription must expire")
	}
	if srv.Notifier().SendAlarm("1", "5", "t", "", "d") {
		t.Fatal("SendAlarm must no-op after expiry")
	}
}

type staticPosition struct{}

func (staticPosition) CurrentPosition() *device.PositionReport {
	return &device.PositionReport{
		Time: "2026-09-15T12:00:02", Longitude: "116.40", Latitude: "39.90",
		Speed: "0.0", Direction: "0.0", Altitude: "50.0",
	}
}

func TestMobilePositionPeriodicReports(t *testing.T) {
	srv, platConn, devAddr := startSubTestServer(t)
	srv.SetPositionSource(staticPosition{})

	sub := subscribeMessage("sub-p", "MobilePosition", 1800)
	sub.Body = "<SUBSCRIBE><CmdType>MobilePosition</CmdType><SN>1</SN>" +
		"<DeviceID>34020000001320000001</DeviceID><Interval>1</Interval></SUBSCRIBE>"
	sub.ContentType = "Application/MANSCDP+xml"
	writeSnapMsg(t, platConn, sub, devAddr)
	readNotify(t, platConn) // 200 OK

	// The Interval=1 loop must deliver a position NOTIFY within ~1.5s.
	platConn.SetReadDeadline(time.Now().Add(2500 * time.Millisecond))
	buf := make([]byte, 65535)
	n, _, err := platConn.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("periodic position NOTIFY not received: %v", err)
	}
	msg, err := device.Parse(buf[:n])
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if msg.Method != "NOTIFY" || msg.Headers["Event"] != "MobilePosition" {
		t.Fatalf("got %v (event %q)", msg.Method, msg.Headers["Event"])
	}
	if !strings.Contains(msg.Body, "<Longitude>116.40</Longitude>") {
		t.Fatalf("position body: %s", msg.Body)
	}
}
