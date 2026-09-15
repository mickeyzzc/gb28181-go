package device_test

// Wire-level tests for the DeviceConfig minimum + ConfigDownload +
// HomePosition control (issue #80 twin of gb28181-rs#69): real UDP
// server, platform MESSAGEs in, 200 OK + Result bodies out, host
// callbacks fired. Wire forms verified against the 2022 standard text
// (A.2.3.2, A.2.4.7, A.2.6.8-9, A.2.3.1.10).

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mickeyzzc/gb28181-go/device"
)

func cfgMessage(callID, sub string) device.SipMessage {
	body := "<?xml version=\"1.0\"?><Control><CmdType>DeviceConfig</CmdType><SN>71</SN>" +
		"<DeviceID>34020000001320000001</DeviceID>" + sub + "</Control>"
	return device.SipMessage{
		Method:      "MESSAGE",
		RequestURI:  "sip:34020000001320000001@3402000000",
		From:        "<sip:34020000002000000001@3402000000>;tag=plat" + callID,
		To:          "<sip:34020000001320000001@3402000000>",
		CallID:      callID,
		CSeq:        "1 MESSAGE",
		Via:         "SIP/2.0/UDP 127.0.0.1:5060;branch=z9hG4bKcfg" + callID,
		ContentType: "Application/MANSCDP+xml",
		Body:        body,
		UserAgent:   "fakeplatform",
		Headers:     map[string]string{},
	}
}

func TestDeviceConfigBasicParamExecutesAndAnswersOK(t *testing.T) {
	var mu sync.Mutex
	var got []string
	platConn, devAddr := startWireTestServer(t, func(srv *device.Server) {
		srv.SetConfigHandlers(device.ConfigCallbacks{
			OnBasicParam: func(p device.BasicParamCfg) {
				mu.Lock()
				defer mu.Unlock()
				got = append(got, p.String())
			},
		})
	})

	writeSnapMsg(t, platConn, cfgMessage("cfg-1",
		"<BasicParam><Name>Dome</Name><Expiration>120</Expiration>"+
			"<HeartBeatInterval>15</HeartBeatInterval><HeartBeatCount>5</HeartBeatCount></BasicParam>"), devAddr)
	ok, _ := readSnapMsg(t, platConn)
	if ok.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", ok.StatusCode)
	}
	resp, _ := readSnapMsg(t, platConn)
	if resp.Method != "MESSAGE" {
		t.Fatalf("second reply = %v, want queued MESSAGE", resp.Method)
	}
	want := "<Response CmdType=\"DeviceConfig\" SN=\"71\"><DeviceID>34020000001320000001</DeviceID><Result>OK</Result></Response>"
	if resp.Body != want {
		t.Fatalf("body = %q, want %q", resp.Body, want)
	}
	waitFor(t, 5*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(got) == 1
	})
	mu.Lock()
	defer mu.Unlock()
	if got[0] != "name=Dome expiration=120 heartbeat_interval=15 heartbeat_count=5" {
		t.Fatalf("callback got %q", got[0])
	}
}

func TestDeviceConfigFrameMirrorAndAlarmReport(t *testing.T) {
	var mu sync.Mutex
	var got []string
	platConn, devAddr := startWireTestServer(t, func(srv *device.Server) {
		srv.SetConfigHandlers(device.ConfigCallbacks{
			OnFrameMirror: func(mode uint32) {
				mu.Lock()
				defer mu.Unlock()
				got = append(got, "mirror")
			},
			OnAlarmReport: func(motion, field uint32) {
				mu.Lock()
				defer mu.Unlock()
				got = append(got, "alarm")
			},
		})
	})

	writeSnapMsg(t, platConn, cfgMessage("cfg-2", "<FrameMirror>2</FrameMirror>"), devAddr)
	ok, _ := readSnapMsg(t, platConn)
	if ok.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", ok.StatusCode)
	}
	resp, _ := readSnapMsg(t, platConn)
	if !strings.Contains(resp.Body, "<Result>OK</Result>") {
		t.Fatalf("body = %q", resp.Body)
	}

	writeSnapMsg(t, platConn, cfgMessage("cfg-3",
		"<AlarmReport><MotionDetection>1</MotionDetection><FieldDetection>0</FieldDetection></AlarmReport>"), devAddr)
	if ok, _ := readSnapMsg(t, platConn); ok.StatusCode != 200 {
		t.Fatalf("200 OK status = %d", ok.StatusCode)
	}
	resp, _ = readSnapMsg(t, platConn)
	if !strings.Contains(resp.Body, "<Result>OK</Result>") {
		t.Fatalf("body = %q", resp.Body)
	}
	waitFor(t, 5*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(got) == 2
	})
}

// Without a config handler the historical reject stands (Result=ERROR).
func TestDeviceConfigWithoutHandlerIsRejected(t *testing.T) {
	platConn, devAddr := startWireTestServer(t, func(*device.Server) {})

	writeSnapMsg(t, platConn, cfgMessage("cfg-4", "<FrameMirror>1</FrameMirror>"), devAddr)
	ok, _ := readSnapMsg(t, platConn)
	if ok.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", ok.StatusCode)
	}
	resp, _ := readSnapMsg(t, platConn)
	want := "<Response CmdType=\"DeviceConfig\" SN=\"71\"><DeviceID>34020000001320000001</DeviceID><Result>ERROR</Result></Response>"
	if resp.Body != want {
		t.Fatalf("body = %q, want %q", resp.Body, want)
	}
}

// HomePosition rides the DeviceControl family (A.2.3.1.10).
func TestDeviceControlHomePositionCallback(t *testing.T) {
	var mu sync.Mutex
	var got string
	platConn, devAddr := startWireTestServer(t, func(srv *device.Server) {
		srv.SetControlHandlers(device.ControlCallbacks{
			OnHomePosition: func(enabled uint32, resetTime, presetIndex *uint32) {
				mu.Lock()
				defer mu.Unlock()
				switch {
				case resetTime != nil && presetIndex != nil:
					got = "full"
				default:
					got = "minimal"
				}
			},
		})
	})

	writeSnapMsg(t, platConn, ctrlMessage("hp-1",
		"<HomePosition><Enabled>1</Enabled><ResetTime>300</ResetTime><PresetIndex>7</PresetIndex></HomePosition>"), devAddr)
	ok, _ := readSnapMsg(t, platConn)
	if ok.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", ok.StatusCode)
	}
	waitFor(t, 5*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return got != ""
	})
	mu.Lock()
	defer mu.Unlock()
	if got != "full" {
		t.Fatalf("callback got %q, want full", got)
	}
}

// ConfigDownload (A.2.4.7/A.2.6.9): BasicParam block when requested
// (values from the live config), bare OK otherwise.
func TestConfigDownloadQueryAnswersMinimal(t *testing.T) {
	platConn, devAddr := startWireTestServer(t, func(*device.Server) {})

	send := func(configType string) device.SipMessage {
		body := "<?xml version=\"1.0\"?><Query><CmdType>ConfigDownload</CmdType><SN>74</SN>" +
			"<DeviceID>34020000001320000001</DeviceID><ConfigType>" + configType + "</ConfigType></Query>"
		writeSnapMsg(t, platConn, device.SipMessage{
			Method:      "MESSAGE",
			RequestURI:  "sip:34020000001320000001@3402000000",
			From:        "<sip:34020000002000000001@3402000000>;tag=platcdl",
			To:          "<sip:34020000001320000001@3402000000>",
			CallID:      "cdl-" + configType,
			CSeq:        "1 MESSAGE",
			Via:         "SIP/2.0/UDP 127.0.0.1:5060;branch=z9hG4bKcdl",
			ContentType: "Application/MANSCDP+xml",
			Body:        body,
			UserAgent:   "fakeplatform",
			Headers:     map[string]string{},
		}, devAddr)
		ok, _ := readSnapMsg(t, platConn)
		if ok.StatusCode != 200 {
			t.Fatalf("status = %d, want 200", ok.StatusCode)
		}
		resp, _ := readSnapMsg(t, platConn)
		return resp
	}

	withBasic := send("BasicParam/FrameMirror")
	if !strings.Contains(withBasic.Body, "<Response CmdType=\"ConfigDownload\" SN=\"74\">") ||
		!strings.Contains(withBasic.Body, "<Result>OK</Result>") ||
		!strings.Contains(withBasic.Body, "<BasicParam><Expiration>") ||
		!strings.Contains(withBasic.Body, "<Expiration>") ||
		!strings.Contains(withBasic.Body, "<HeartBeatInterval>") ||
		!strings.Contains(withBasic.Body, "<HeartBeatCount>") {
		t.Fatalf("configdownload body: %q", withBasic.Body)
	}
	bare := send("OSDConfig")
	if !strings.Contains(bare.Body, "<Result>OK</Result>") || strings.Contains(bare.Body, "<BasicParam>") {
		t.Fatalf("bare body: %q", bare.Body)
	}
}
