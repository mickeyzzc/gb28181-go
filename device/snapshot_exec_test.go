package device_test

// Device-side snapshot command execution (mibee-eye-raspi#28 / GB/T
// 28181-2022 A.2.1.24 + A.2.5.7): a DeviceControl(SnapShot) MESSAGE is
// answered 200, handed to the installed SnapshotExecutor, and completes
// asynchronously with an UploadSnapShotFinished notify echoing the
// SessionID. Without an executor the historical control-reject behavior
// is preserved.

import (
	"context"
	"errors"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mickeyzzc/gb28181-go/device"
	"github.com/mickeyzzc/gb28181-go/manscdp"
)

const (
	snapSessionID = "0123456789abcdef0123456789abcdef"
	snapUploadURL = "http://192.168.63.30:9090/api/gb28181/snapshot/upload?session=0123456789abcdef0123456789abcdef"
)

// snapStubAuth accepts the REGISTER flow silently.
type snapStubAuth struct{}

func (snapStubAuth) InitialAuthorization() string                  { return "" }
func (snapStubAuth) AuthorizeWithChallenge(string) (string, error) { return "", nil }
func (snapStubAuth) VerifyOK(string) error                         { return nil }

// okExec records the parsed command and returns a fixed ID list.
type okExec struct {
	mu  sync.Mutex
	cmd *manscdp.SnapShotCmd
	ids []string
}

func (e *okExec) Execute(_ context.Context, cmd manscdp.SnapShotCmd) ([]string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	c := cmd
	e.cmd = &c
	return e.ids, nil
}

// errExec always fails the exchange.
type errExec struct{}

func (errExec) Execute(context.Context, manscdp.SnapShotCmd) ([]string, error) {
	return nil, errors.New("stub: capture failed")
}

// startSnapTestServer boots a full device.Server against a fake platform
// socket and quiets the REGISTER lifecycle with a bare 200 OK.
func startSnapTestServer(t *testing.T, exec device.SnapshotExecutor) (*net.UDPConn, *net.UDPAddr) {
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
	if exec != nil {
		srv.SetSnapshotExecutor(exec)
	}

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

	return platConn, peer
}

func readSnapMsg(t *testing.T, conn *net.UDPConn) (device.SipMessage, *net.UDPAddr) {
	t.Helper()

	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
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

func writeSnapMsg(t *testing.T, conn *net.UDPConn, msg device.SipMessage, peer *net.UDPAddr) {
	t.Helper()

	if _, err := conn.WriteToUDP(msg.Serialize(), peer); err != nil {
		t.Fatalf("fake platform write: %v", err)
	}
}

// platformSnapshotControl builds the DeviceControl(SnapShot) request.
func platformSnapshotControl(callID string) device.SipMessage {
	body := "<Control><CmdType>DeviceControl</CmdType><SN>18</SN>" +
		"<DeviceID>34020000001320000001</DeviceID>" +
		"<SnapShot><SnapNum>3</SnapNum><Interval>2</Interval>" +
		"<UploadURL>" + snapUploadURL + "</UploadURL>" +
		"<SessionID>" + snapSessionID + "</SessionID></SnapShot></Control>"
	return device.SipMessage{
		Method:      "MESSAGE",
		RequestURI:  "sip:34020000001320000001@3402000000",
		From:        "<sip:34020000002000000001@3402000000>;tag=plat" + strconv.FormatInt(time.Now().UnixNano(), 36),
		To:          "<sip:34020000001320000001@3402000000>",
		CallID:      callID,
		CSeq:        "1 MESSAGE",
		Via:         "SIP/2.0/UDP 127.0.0.1:5060;branch=z9hG4bKsnap" + callID,
		ContentType: "Application/MANSCDP+xml",
		Body:        body,
		UserAgent:   "fakeplatform",
		Headers:     map[string]string{},
	}
}

func TestSnapshotCommandExecutesAndNotifiesWithFileIDs(t *testing.T) {
	exec := &okExec{ids: []string{"store/2026/09/09/a.jpg", "store/2026/09/09/b.jpg"}}
	platConn, peer := startSnapTestServer(t, exec)

	writeSnapMsg(t, platConn, platformSnapshotControl("snap1"), peer)

	// 1) The MESSAGE transaction is answered 200 synchronously.
	ok, _ := readSnapMsg(t, platConn)
	if ok.StatusCode != 200 {
		t.Fatalf("status = %d, want 200 (msg: %v)", ok.StatusCode, ok)
	}

	// 2) The completion notify arrives asynchronously, echoing the
	//    SessionID and the uploaded-file IDs (child-element format, the
	//    cross-twin golden).
	notify, _ := readSnapMsg(t, platConn)
	if notify.Method != "MESSAGE" {
		t.Fatalf("method = %q, want MESSAGE (msg: %v)", notify.Method, notify)
	}
	body := notify.Body
	if !strings.Contains(body, "<CmdType>UploadSnapShotFinished</CmdType>") {
		t.Fatalf("notify body: %s", body)
	}
	if !strings.Contains(body, "<SessionID>"+snapSessionID+"</SessionID>") {
		t.Fatalf("notify body: %s", body)
	}
	if got := strings.Count(body, "<SnapShotFileID>"); got != 2 {
		t.Fatalf("SnapShotFileID count = %d, want 2 (body: %s)", got, body)
	}

	// 3) The executor saw the fully parsed command.
	exec.mu.Lock()
	defer exec.mu.Unlock()
	if exec.cmd == nil {
		t.Fatal("executor never ran")
	}
	if exec.cmd.SnapNum != 3 || exec.cmd.Interval != 2 ||
		exec.cmd.UploadURL != snapUploadURL || exec.cmd.SessionID != snapSessionID {
		t.Fatalf("parsed command = %+v", *exec.cmd)
	}
}

func TestFailedExchangeNotifiesWithEmptyList(t *testing.T) {
	platConn, peer := startSnapTestServer(t, errExec{})

	writeSnapMsg(t, platConn, platformSnapshotControl("snap2"), peer)

	ok, _ := readSnapMsg(t, platConn)
	if ok.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", ok.StatusCode)
	}

	notify, _ := readSnapMsg(t, platConn)
	if notify.Method != "MESSAGE" {
		t.Fatalf("method = %q, want MESSAGE", notify.Method)
	}
	body := notify.Body
	if !strings.Contains(body, "<CmdType>UploadSnapShotFinished</CmdType>") {
		t.Fatalf("notify body: %s", body)
	}
	if !strings.Contains(body, "<SessionID>"+snapSessionID+"</SessionID>") {
		t.Fatalf("notify body: %s", body)
	}
	if got := strings.Count(body, "<SnapShotFileID>"); got != 0 {
		t.Fatalf("SnapShotFileID count = %d, want 0 (body: %s)", got, body)
	}
}

func TestWithoutExecutorTheControlIsExplicitlyRejected(t *testing.T) {
	platConn, peer := startSnapTestServer(t, nil)

	writeSnapMsg(t, platConn, platformSnapshotControl("snap3"), peer)

	ok, _ := readSnapMsg(t, platConn)
	if ok.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", ok.StatusCode)
	}

	// Without an executor the control is explicitly rejected (fast
	// failure for the platform — previously parse-warn + silence).
	reject, _ := readSnapMsg(t, platConn)
	if reject.Method != "MESSAGE" {
		t.Fatalf("method = %q, want MESSAGE", reject.Method)
	}
	body := reject.Body
	if !strings.Contains(body, "DeviceControl") || !strings.Contains(body, "ERROR") {
		t.Fatalf("reject body: %s", body)
	}
}
