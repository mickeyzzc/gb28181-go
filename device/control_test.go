package device_test

// Wire-level tests for §9.3.2 DeviceControl sub-command dispatch
// (issue #81): a real device Server over a real UDP socket, platform
// MESSAGEs in, 200/reject MESSAGEs out — the same harness style as the
// snapshot executor tests, and the Go twin of gb28181-rs #63's golden
// suite.

import (
	"context"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mickeyzzc/gb28181-go/device"
)

// ctrlRecorder captures which callbacks fired and with what arguments.
type ctrlRecorder struct {
	mu    sync.Mutex
	calls []string
}

func (r *ctrlRecorder) add(s string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, s)
}

func (r *ctrlRecorder) latest() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.calls) == 0 {
		return ""
	}
	return r.calls[len(r.calls)-1]
}

func startCtrlTestServer(t *testing.T, cbs device.ControlCallbacks) (*net.UDPConn, *net.UDPAddr) {
	t.Helper()
	return startWireTestServer(t, func(srv *device.Server) {
		srv.SetControlHandlers(cbs)
	})
}

// startWireTestServer boots a real device Server over real UDP sockets and
// completes the REGISTER handshake — the shared harness for wire-level
// platform→device tests (controls, talkback, …). configure installs the
// per-suite host seams before Start.
func startWireTestServer(t *testing.T, configure func(*device.Server)) (*net.UDPConn, *net.UDPAddr) {
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
	configure(srv)

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

	devAddr := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: sipPort}
	return platConn, devAddr
}

func ctrlMessage(callID, sub string) device.SipMessage {
	body := "<?xml version=\"1.0\"?><Control><CmdType>DeviceControl</CmdType><SN>17</SN>" +
		"<DeviceID>34020000001320000001</DeviceID>" + sub + "</Control>"
	return device.SipMessage{
		Method:      "MESSAGE",
		RequestURI:  "sip:34020000001320000001@3402000000",
		From:        "<sip:34020000002000000001@3402000000>;tag=ctrl" + callID,
		To:          "<sip:34020000001320000001@3402000000>",
		CallID:      callID,
		CSeq:        "1 MESSAGE",
		Via:         "SIP/2.0/UDP 127.0.0.1:5060;branch=z9hG4bKctrl" + callID,
		ContentType: "Application/MANSCDP+xml",
		Body:        body,
		UserAgent:   "fakeplatform",
		Headers:     map[string]string{},
	}
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("condition not met within %v", timeout)
}

func TestDeviceControlSubCommandsExecuteCallbacks(t *testing.T) {
	rec := &ctrlRecorder{}
	cbs := device.ControlCallbacks{
		OnForceIFrame: func() { rec.add("iframe") },
		OnRecordCmd:   func(start bool) { rec.add("record:" + strconv.FormatBool(start)) },
		OnGuardCmd:    func(arm bool) { rec.add("guard:" + strconv.FormatBool(arm)) },
		OnResetAlarm:  func() { rec.add("alarm") },
		OnTeleBoot:    func() { rec.add("boot") },
		OnPTZCmd:      func(cmd device.PtzCommand) { rec.add("ptz:" + cmd.RawHex) },
		OnDragZoom: func(cmd device.DragZoom) {
			rec.add("dragzoom:" + strconv.FormatBool(cmd.ZoomIn) + ":" +
				strconv.Itoa(cmd.Length) + "," + strconv.Itoa(cmd.Width) + "," +
				strconv.Itoa(cmd.MidPointX) + "," + strconv.Itoa(cmd.MidPointY) + "," +
				strconv.Itoa(cmd.LengthX) + "," + strconv.Itoa(cmd.LengthY))
		},
	}
	platConn, devAddr := startCtrlTestServer(t, cbs)

	for i, tc := range []struct {
		name string
		sub  string
		want string
	}{
		{"iframe", "<IFrameCmd>Send</IFrameCmd>", "iframe"},
		{"record on", "<RecordCmd>Record</RecordCmd>", "record:true"},
		{"record off", "<RecordCmd>StopRecord</RecordCmd>", "record:false"},
		{"guard on", "<GuardCmd>SetGuard</GuardCmd>", "guard:true"},
		{"guard off", "<GuardCmd>ResetGuard</GuardCmd>", "guard:false"},
		{"alarm", "<AlarmCmd>ResetAlarm</AlarmCmd>", "alarm"},
		{"teleboot", "<TeleBoot>Boot</TeleBoot>", "boot"},
		{"ptz decoded", "<PTZCmd>A50F0102200000D7</PTZCmd>", "ptz:A50F0102200000D7"},
		{
			"dragzoom in", "<DragZoomIn><Length>1920</Length><Width>1080</Width>" +
				"<MidPointX>960</MidPointX><MidPointY>540</MidPointY>" +
				"<LengthX>480</LengthX><LengthY>270</LengthY></DragZoomIn>",
			"dragzoom:true:1920,1080,960,540,480,270",
		},
		{
			"dragzoom out", "<DragZoomOut><Length>1920</Length><Width>1080</Width>" +
				"<MidPointX>960</MidPointX><MidPointY>540</MidPointY>" +
				"<LengthX>480</LengthX><LengthY>270</LengthY></DragZoomOut>",
			"dragzoom:false:1920,1080,960,540,480,270",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			callID := "ctrl" + strconv.Itoa(i)
			writeSnapMsg(t, platConn, ctrlMessage(callID, tc.sub), devAddr)
			ok, _ := readSnapMsg(t, platConn)
			if ok.StatusCode != 200 {
				t.Fatalf("status = %d, want 200 (msg: %v)", ok.StatusCode, ok)
			}
			// The 200 OK precedes the callback (it is sent by the common
			// MESSAGE path), so wait for the effect rather than assert it
			// immediately.
			waitFor(t, time.Second, func() bool { return rec.latest() == tc.want })
		})
	}
}

func TestDeviceControlWithoutCallbackIsRejected(t *testing.T) {
	// No handlers installed: the sub-command must get the explicit
	// control reject (parity with the Rust twin's no-handler behavior).
	platConn, devAddr := startCtrlTestServer(t, device.ControlCallbacks{})

	writeSnapMsg(t, platConn, ctrlMessage("ctrlreject", "<IFrameCmd>Send</IFrameCmd>"), devAddr)
	ok, _ := readSnapMsg(t, platConn)
	if ok.StatusCode != 200 {
		t.Fatalf("status = %d, want 200 (msg: %v)", ok.StatusCode, ok)
	}
	reject, _ := readSnapMsg(t, platConn)
	if reject.Method != "MESSAGE" || !strings.Contains(reject.Body, "<Result>ERROR</Result>") {
		t.Fatalf("expected ControlReject MESSAGE, got %v (body: %s)", reject, reject.Body)
	}
	if !strings.Contains(reject.Body, `CmdType="DeviceControl"`) {
		t.Fatalf("reject body missing DeviceControl echo: %s", reject.Body)
	}
}

func TestDragZoomMissingChildIsRejected(t *testing.T) {
	// A.2.3.1.8/9 mark all six children 必选 — a body missing one gets
	// the control reject even with the callback installed (parity with
	// the Rust twin's strict reading).
	seen := make(chan struct{}, 1)
	cbs := device.ControlCallbacks{
		OnDragZoom: func(device.DragZoom) { seen <- struct{}{} },
	}
	platConn, devAddr := startCtrlTestServer(t, cbs)

	writeSnapMsg(t, platConn, ctrlMessage("dzmissing",
		"<DragZoomIn><Length>1920</Length><Width>1080</Width>"+
			"<MidPointX>960</MidPointX><MidPointY>540</MidPointY>"+
			"<LengthX>480</LengthX></DragZoomIn>"), devAddr)
	ok, _ := readSnapMsg(t, platConn)
	if ok.StatusCode != 200 {
		t.Fatalf("status = %d, want 200 (msg: %v)", ok.StatusCode, ok)
	}
	reject, _ := readSnapMsg(t, platConn)
	if reject.Method != "MESSAGE" || !strings.Contains(reject.Body, "<Result>ERROR</Result>") {
		t.Fatalf("expected ControlReject MESSAGE, got %v (body: %s)", reject, reject.Body)
	}
	select {
	case <-seen:
		t.Fatal("OnDragZoom must not fire for a body missing LengthY")
	default:
	}
}
