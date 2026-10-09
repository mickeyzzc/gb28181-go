package device_test

// Wire-level tests for the remaining 2022 DeviceControl sub-commands
// (issue #108): DeviceUpgrade (A.2.3.1.12 + the A.2.5.9 result notify),
// FormatSDCard (A.2.3.1.13) and PTZPreciseCtrl (A.2.3.1.11 / A.2.1.11).
// Wire forms verified against the 2022 standard text.

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mickeyzzc/gb28181-go/device"
	"github.com/mickeyzzc/gb28181-go/manscdp"
)

const upgradeSession = "0123456789abcdef0123456789abcdef"

// okUpgrader records the command and reports success.
type okUpgrader struct {
	mu  sync.Mutex
	cmd *manscdp.DeviceUpgradeCmd
}

func (u *okUpgrader) Upgrade(_ context.Context, cmd manscdp.DeviceUpgradeCmd) (device.DeviceUpgradeOutcome, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	c := cmd
	u.cmd = &c
	return device.DeviceUpgradeOutcome{Success: true, Firmware: "v9.9.9"}, nil
}

// failUpgrader reports the 02 (package corrupt) failure path.
type failUpgrader struct{}

func (failUpgrader) Upgrade(context.Context, manscdp.DeviceUpgradeCmd) (device.DeviceUpgradeOutcome, error) {
	return device.DeviceUpgradeOutcome{Success: false, Firmware: "v1.0.0", FailedReason: "02"}, nil
}

func upgradeMessage(callID string) device.SipMessage {
	body := "<Control><CmdType>DeviceControl</CmdType><SN>21</SN>" +
		"<DeviceID>34020000001320000001</DeviceID>" +
		"<DeviceUpgrade><Firmware>v1.0.0</Firmware>" +
		"<FileURL>http://192.168.63.30/firmware.bin</FileURL>" +
		"<Manufacturer>MiBee</Manufacturer>" +
		"<SessionID>" + upgradeSession + "</SessionID></DeviceUpgrade></Control>"
	return device.SipMessage{
		Method:      "MESSAGE",
		RequestURI:  "sip:34020000001320000001@3402000000",
		From:        "<sip:34020000002000000001@3402000000>;tag=platupg" + callID,
		To:          "<sip:34020000001320000001@3402000000>",
		CallID:      callID,
		CSeq:        "1 MESSAGE",
		Via:         "SIP/2.0/UDP 127.0.0.1:5060;branch=z9hG4bKupg" + callID,
		ContentType: "Application/MANSCDP+xml",
		Body:        body,
		UserAgent:   "fakeplatform",
		Headers:     map[string]string{},
	}
}

func TestDeviceUpgradeExecutesAndNotifiesResult(t *testing.T) {
	upg := &okUpgrader{}
	platConn, devAddr := startWireTestServer(t, func(srv *device.Server) {
		srv.SetDeviceUpgrader(upg)
	})

	writeSnapMsg(t, platConn, upgradeMessage("upg-1"), devAddr)
	ok, _ := readSnapMsg(t, platConn)
	if ok.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", ok.StatusCode)
	}
	notify, _ := readSnapMsg(t, platConn)
	if notify.Method != "MESSAGE" {
		t.Fatalf("method = %q (msg: %v)", notify.Method, notify)
	}
	body := notify.Body
	if !strings.Contains(body, "<CmdType>DeviceUpgradeResult</CmdType>") ||
		!strings.Contains(body, "<SessionID>"+upgradeSession+"</SessionID>") ||
		!strings.Contains(body, "<UpgradeResult>OK</UpgradeResult>") ||
		!strings.Contains(body, "<Firmware>v9.9.9</Firmware>") ||
		strings.Contains(body, "UpgradeFailedReason") {
		t.Fatalf("notify body: %s", body)
	}

	upg.mu.Lock()
	defer upg.mu.Unlock()
	if upg.cmd == nil || upg.cmd.FileURL != "http://192.168.63.30/firmware.bin" ||
		upg.cmd.Firmware != "v1.0.0" || upg.cmd.Manufacturer != "MiBee" ||
		upg.cmd.SessionID != upgradeSession {
		t.Fatalf("upgrader saw %+v", upg.cmd)
	}
}

func TestDeviceUpgradeFailureNotifiesWithError(t *testing.T) {
	platConn, devAddr := startWireTestServer(t, func(srv *device.Server) {
		srv.SetDeviceUpgrader(failUpgrader{})
	})

	writeSnapMsg(t, platConn, upgradeMessage("upg-2"), devAddr)
	if ok, _ := readSnapMsg(t, platConn); ok.StatusCode != 200 {
		t.Fatalf("status = %d", ok.StatusCode)
	}
	notify, _ := readSnapMsg(t, platConn)
	if !strings.Contains(notify.Body, "<UpgradeResult>ERROR</UpgradeResult>") ||
		!strings.Contains(notify.Body, "<UpgradeFailedReason>02</UpgradeFailedReason>") {
		t.Fatalf("notify body: %s", notify.Body)
	}
}

// Without an upgrader the control is explicitly rejected.
func TestDeviceUpgradeWithoutUpgraderIsRejected(t *testing.T) {
	platConn, devAddr := startWireTestServer(t, func(*device.Server) {})

	writeSnapMsg(t, platConn, upgradeMessage("upg-3"), devAddr)
	if ok, _ := readSnapMsg(t, platConn); ok.StatusCode != 200 {
		t.Fatalf("status = %d", ok.StatusCode)
	}
	reject, _ := readSnapMsg(t, platConn)
	if !strings.Contains(reject.Body, `CmdType="DeviceControl"`) || !strings.Contains(reject.Body, "ERROR") {
		t.Fatalf("reject body: %s", reject.Body)
	}
}

// FormatSDCard (A.2.3.1.13): integer element — 0 formats every card.
func TestFormatSDCardCallback(t *testing.T) {
	var mu sync.Mutex
	var cards []int
	platConn, devAddr := startWireTestServer(t, func(srv *device.Server) {
		srv.SetControlHandlers(device.ControlCallbacks{
			OnFormatSDCard: func(card int) {
				mu.Lock()
				defer mu.Unlock()
				cards = append(cards, card)
			},
		})
	})

	send := func(sub string) {
		writeSnapMsg(t, platConn, ctrlMessage("fmt", sub), devAddr)
		ok, _ := readSnapMsg(t, platConn)
		if ok.StatusCode != 200 {
			t.Fatalf("status = %d", ok.StatusCode)
		}
	}
	send("<FormatSDCard>0</FormatSDCard>")
	send("<FormatSDCard>2</FormatSDCard>")
	waitFor(t, 5*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(cards) == 2
	})
	mu.Lock()
	defer mu.Unlock()
	if cards[0] != 0 || cards[1] != 2 {
		t.Fatalf("cards = %v", cards)
	}
}

// PTZPreciseCtrl (A.2.3.1.11 / A.2.1.11): optional Pan/Tilt/Zoom doubles.
func TestPTZPreciseCtrlCallback(t *testing.T) {
	var mu sync.Mutex
	var got *manscdp.PTZPreciseCmd
	platConn, devAddr := startWireTestServer(t, func(srv *device.Server) {
		srv.SetControlHandlers(device.ControlCallbacks{
			OnPTZPrecise: func(p manscdp.PTZPreciseCmd) {
				mu.Lock()
				defer mu.Unlock()
				c := p
				got = &c
			},
		})
	})

	writeSnapMsg(t, platConn, ctrlMessage("ppc-1",
		"<PTZPreciseCtrl><Pan>180.50</Pan><Tilt>-12.25</Tilt><Zoom>4.00</Zoom></PTZPreciseCtrl>"), devAddr)
	ok, _ := readSnapMsg(t, platConn)
	if ok.StatusCode != 200 {
		t.Fatalf("status = %d", ok.StatusCode)
	}
	waitFor(t, 5*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return got != nil
	})
	mu.Lock()
	pan, tilt, zoom := got.Pan, got.Tilt, got.Zoom
	mu.Unlock()
	if pan == nil || *pan != 180.50 || tilt == nil || *tilt != -12.25 ||
		zoom == nil || *zoom != 4.00 {
		t.Fatalf("decoded = %+v", got)
	}

	// All-absent precise control still decodes (empty command).
	writeSnapMsg(t, platConn, ctrlMessage("ppc-2", "<PTZPreciseCtrl></PTZPreciseCtrl>"), devAddr)
	waitFor(t, 5*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return got.Pan == nil && got.Tilt == nil && got.Zoom == nil
	})
}
