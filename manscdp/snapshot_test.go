package manscdp

import (
	"encoding/xml"
	"strings"
	"testing"
)

// Goldens pinned to the GB/T 28181-2022 text (A.2.1.24 snapShotCfgType,
// A.2.5.7 UploadSnapShotFinished): the snapshot rides the device-config
// channel — a Control root whose CmdType is DeviceConfig carries the
// <SnapShotConfig> element with children SnapNum/Interval/UploadURL/
// SessionID; the completion notify carries SessionID and a SnapShotList
// of SnapShotFileID entries.

const goldenSessionID = "0123456789abcdef0123456789abcdef"

func TestDeviceConfigSnapShotConfigGolden(t *testing.T) {
	dc := DeviceConfig{
		CmdType:  CmdDeviceConfig,
		SN:       17,
		DeviceID: "34020000001320000001",
		SnapShotConfig: &SnapShotCmd{
			SnapNum:   3,
			Interval:  2,
			UploadURL: "http://192.168.63.30:9090/api/gb28181/snapshot/upload",
			SessionID: goldenSessionID,
		},
	}
	out, err := xml.Marshal(dc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := "<Control><CmdType>DeviceConfig</CmdType><SN>17</SN><DeviceID>34020000001320000001</DeviceID>" +
		"<SnapShotConfig><SnapNum>3</SnapNum><Interval>2</Interval>" +
		"<UploadURL>http://192.168.63.30:9090/api/gb28181/snapshot/upload</UploadURL>" +
		"<SessionID>" + goldenSessionID + "</SessionID></SnapShotConfig></Control>"
	if string(out) != want {
		t.Fatalf("config XML mismatch:\n got: %s\nwant: %s", out, want)
	}

	// Manual snapshot (single frame) omits Interval per the schema
	// (Interval is optional).
	manual := dc
	manual.SnapShotConfig = &SnapShotCmd{SnapNum: 1, UploadURL: "http://x/u", SessionID: goldenSessionID}
	out, err = xml.Marshal(manual)
	if err != nil {
		t.Fatalf("marshal manual: %v", err)
	}
	if want := "<Control><CmdType>DeviceConfig</CmdType><SN>17</SN><DeviceID>34020000001320000001</DeviceID>" +
		"<SnapShotConfig><SnapNum>1</SnapNum><UploadURL>http://x/u</UploadURL>" +
		"<SessionID>" + goldenSessionID + "</SessionID></SnapShotConfig></Control>"; string(out) != want {
		t.Fatalf("manual config XML mismatch:\n got: %s\nwant: %s", out, want)
	}
}

// TestDecodeSnapShotConfigRealCapture pins the decode of a real 2022
// platform capture (issue #107): CmdType is DeviceConfig and the payload
// element is SnapShotConfig — not DeviceControl/SnapShot as previously
// modeled.
func TestDecodeSnapShotConfigRealCapture(t *testing.T) {
	body := "<Control><CmdType>DeviceConfig</CmdType><SN>10003</SN>" +
		"<DeviceID>34020000001310000001</DeviceID>" +
		"<SnapShotConfig><SnapNum>1</SnapNum><Interval>1</Interval>" +
		"<UploadURL>http://192.168.0.110:9999/snap</UploadURL>" +
		"<SessionID>3cffba1f5df14a47acb6594b2375c5c7</SessionID></SnapShotConfig></Control>"
	ct, v, err := Decode([]byte(body))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if ct != CmdDeviceConfig {
		t.Fatalf("CmdType = %q, want DeviceConfig", ct)
	}
	cfg, ok := v.(DeviceConfig)
	if !ok {
		t.Fatalf("decoded type = %T", v)
	}
	if cfg.SN != 10003 || cfg.DeviceID != "34020000001310000001" {
		t.Fatalf("header fields = %+v", cfg)
	}
	if cfg.SnapShotConfig == nil {
		t.Fatal("SnapShotConfig = nil")
	}
	want := SnapShotCmd{SnapNum: 1, Interval: 1, UploadURL: "http://192.168.0.110:9999/snap", SessionID: "3cffba1f5df14a47acb6594b2375c5c7"}
	if *cfg.SnapShotConfig != want {
		t.Fatalf("SnapShotConfig = %+v, want %+v", *cfg.SnapShotConfig, want)
	}
}

func TestDecodeUploadSnapShotFinished(t *testing.T) {
	body := "<Notify><CmdType>UploadSnapShotFinished</CmdType><SN>18</SN>" +
		"<DeviceID>34020000001320000001</DeviceID><SessionID>" + goldenSessionID + "</SessionID>" +
		"<SnapShotList><SnapShotFileID>f-1</SnapShotFileID><SnapShotFileID>f-2</SnapShotFileID></SnapShotList></Notify>"
	cmdType, out, err := Decode([]byte(body))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if cmdType != CmdUploadSnapShotFinished {
		t.Fatalf("CmdType = %q, want UploadSnapShotFinished", cmdType)
	}
	n, ok := out.(UploadSnapShotFinished)
	if !ok {
		t.Fatalf("decoded type = %T", out)
	}
	if n.SN != 18 || n.DeviceID != "34020000001320000001" || n.SessionID != goldenSessionID {
		t.Fatalf("fields = %+v", n)
	}
	if len(n.SnapShotList) != 2 || n.SnapShotList[0] != "f-1" || n.SnapShotList[1] != "f-2" {
		t.Fatalf("SnapShotList = %+v", n.SnapShotList)
	}

	// Zero file IDs (schema minOccurs=0) parse as an empty list — that is
	// the "all uploads failed" signal, not a decode error.
	empty := "<Notify><CmdType>UploadSnapShotFinished</CmdType><SN>1</SN><DeviceID>d</DeviceID>" +
		"<SessionID>" + goldenSessionID + "</SessionID><SnapShotList></SnapShotList></Notify>"
	_, out2, err := Decode([]byte(empty))
	if err != nil {
		t.Fatalf("decode empty list: %v", err)
	}
	if n2 := out2.(UploadSnapShotFinished); len(n2.SnapShotList) != 0 {
		t.Fatalf("empty SnapShotList = %+v", n2.SnapShotList)
	}
}

func TestUploadSnapShotFinishedRoundTrip(t *testing.T) {
	n := BuildUploadSnapShotFinished(18, "34020000001320000001", goldenSessionID, []string{"f-1", "f-2"})
	out, err := xml.Marshal(n)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := "<Notify><CmdType>UploadSnapShotFinished</CmdType><SN>18</SN>" +
		"<DeviceID>34020000001320000001</DeviceID><SessionID>" + goldenSessionID + "</SessionID>" +
		"<SnapShotList><SnapShotFileID>f-1</SnapShotFileID><SnapShotFileID>f-2</SnapShotFileID></SnapShotList></Notify>"
	if string(out) != want {
		t.Fatalf("notify XML mismatch:\n got: %s\nwant: %s", out, want)
	}
	// The platform re-parses the device's report byte-identically.
	_, out2, err := Decode(out)
	if err != nil {
		t.Fatalf("re-decode: %v", err)
	}
	got := out2.(UploadSnapShotFinished)
	if got.SN != n.SN || got.DeviceID != n.DeviceID || got.SessionID != n.SessionID ||
		len(got.SnapShotList) != len(n.SnapShotList) {
		t.Fatalf("round-trip mismatch:\n got: %+v\nwant: %+v", got, n)
	}
	for i := range got.SnapShotList {
		if got.SnapShotList[i] != n.SnapShotList[i] {
			t.Fatalf("round-trip fileID[%d] mismatch", i)
		}
	}
}

func TestDeviceUpgradeResultRoundTrip(t *testing.T) {
	n := BuildDeviceUpgradeResult(31, "34020000001320000001", goldenSessionID,
		false, "v1.0.0", "02")
	out, err := xml.Marshal(n)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := "<Notify><CmdType>DeviceUpgradeResult</CmdType><SN>31</SN>" +
		"<DeviceID>34020000001320000001</DeviceID><SessionID>" + goldenSessionID + "</SessionID>" +
		"<UpgradeResult>ERROR</UpgradeResult><Firmware>v1.0.0</Firmware>" +
		"<UpgradeFailedReason>02</UpgradeFailedReason></Notify>"
	if string(out) != want {
		t.Fatalf("notify XML mismatch:\n got: %s\nwant: %s", out, want)
	}
	_, out2, err := Decode(out)
	if err != nil {
		t.Fatalf("re-decode: %v", err)
	}
	got := out2.(DeviceUpgradeResult)
	if got.UpgradeResult != "ERROR" || got.Firmware != "v1.0.0" ||
		got.UpgradeFailedReason != "02" || got.SessionID != goldenSessionID {
		t.Fatalf("round-trip mismatch: %+v", got)
	}

	// Success omits the reason.
	ok := BuildDeviceUpgradeResult(32, "d", goldenSessionID, true, "v9.9.9", "")
	out, err = xml.Marshal(ok)
	if err != nil {
		t.Fatalf("marshal ok: %v", err)
	}
	if !strings.Contains(string(out), "<UpgradeResult>OK</UpgradeResult>") ||
		strings.Contains(string(out), "UpgradeFailedReason") {
		t.Fatalf("ok notify XML: %s", out)
	}
}
