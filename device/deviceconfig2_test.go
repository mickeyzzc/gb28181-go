package device_test

// Wire-level tests for the remaining DeviceConfig sub-commands (issue
// #109, twin of the gb28181-rs follow-up): VideoParamAttribute,
// VideoRecordPlan, VideoAlarmRecord, PictureMask, OSDConfig decode and
// fire their optional host callbacks; ConfigDownload grows the
// VideoParamOpt block. Wire forms verified against the 2022 standard
// text (A.2.3.2.5-8, A.2.3.2.11, A.2.1.12-17, A.2.1.20).

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mickeyzzc/gb28181-go/device"
	"github.com/mickeyzzc/gb28181-go/manscdp"
)

// TestDeviceConfigVideoParamAttribute pins A.2.1.13: an Item list keyed
// by stream number, values per Annex G (SDP f=).
func TestDeviceConfigVideoParamAttribute(t *testing.T) {
	var mu sync.Mutex
	var got *manscdp.VideoParamAttributeCmd
	platConn, devAddr := startWireTestServer(t, func(srv *device.Server) {
		srv.SetConfigHandlers(device.ConfigCallbacks{
			OnVideoParamAttribute: func(p manscdp.VideoParamAttributeCmd) {
				mu.Lock()
				defer mu.Unlock()
				c := p
				got = &c
			},
		})
	})

	writeSnapMsg(t, platConn, cfgMessage("vpa-1",
		"<VideoParamAttribute Num=\"2\">"+
			"<Item><StreamNumber>0</StreamNumber><VideoFormat>H.264</VideoFormat>"+
			"<Resolution>1920x1080</Resolution><FrameRate>25</FrameRate>"+
			"<BitRateType>0</BitRateType><VideoBitRate>4096</VideoBitRate></Item>"+
			"<Item><StreamNumber>1</StreamNumber><VideoFormat>H.265</VideoFormat>"+
			"<Resolution>640x480</Resolution><FrameRate>15</FrameRate>"+
			"<BitRateType>1</BitRateType></Item>"+
			"</VideoParamAttribute>"), devAddr)
	ok, _ := readSnapMsg(t, platConn)
	if ok.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", ok.StatusCode)
	}
	resp, _ := readSnapMsg(t, platConn)
	if !strings.Contains(resp.Body, "<Result>OK</Result>") {
		t.Fatalf("body = %q", resp.Body)
	}
	waitFor(t, 5*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return got != nil
	})
	mu.Lock()
	defer mu.Unlock()
	if got.Num != 2 || len(got.Items) != 2 {
		t.Fatalf("decoded = %+v", got)
	}
	main := got.Items[0]
	if main.StreamNumber != 0 || main.VideoFormat != "H.264" ||
		main.Resolution != "1920x1080" || main.FrameRate != "25" ||
		main.BitRateType != "0" || main.VideoBitRate != "4096" {
		t.Fatalf("main item = %+v", main)
	}
	if got.Items[1].VideoBitRate != "" {
		t.Fatalf("absent VideoBitRate must decode empty, got %+v", got.Items[1])
	}
}

// TestDeviceConfigVideoRecordPlan pins A.2.1.15: weekly schedule with
// up to 8 time segments per day.
func TestDeviceConfigVideoRecordPlan(t *testing.T) {
	var mu sync.Mutex
	var got *manscdp.VideoRecordPlanCmd
	platConn, devAddr := startWireTestServer(t, func(srv *device.Server) {
		srv.SetConfigHandlers(device.ConfigCallbacks{
			OnVideoRecordPlan: func(p manscdp.VideoRecordPlanCmd) {
				mu.Lock()
				defer mu.Unlock()
				c := p
				got = &c
			},
		})
	})

	writeSnapMsg(t, platConn, cfgMessage("vrp-1",
		"<VideoRecordPlan><RecordEnable>1</RecordEnable><RecordScheduleSumNum>2</RecordScheduleSumNum>"+
			"<RecordSchedule><WeekDayNum>1</WeekDayNum><TimeSegmentSumNum>1</TimeSegmentSumNum>"+
			"<TimeSegment><StartHour>0</StartHour><StartMin>0</StartMin><StartSec>0</StartSec>"+
			"<StopHour>12</StopHour><StopMin>0</StopMin><StopSec>0</StopSec></TimeSegment></RecordSchedule>"+
			"<RecordSchedule><WeekDayNum>7</WeekDayNum><TimeSegmentSumNum>2</TimeSegmentSumNum>"+
			"<TimeSegment><StartHour>8</StartHour><StartMin>30</StartMin><StartSec>0</StartSec>"+
			"<StopHour>11</StopHour><StopMin>45</StopMin><StopSec>30</StopSec></TimeSegment>"+
			"<TimeSegment><StartHour>14</StartHour><StartMin>0</StartMin><StartSec>0</StartSec>"+
			"<StopHour>23</StopHour><StopMin>59</StopMin><StopSec>59</StopSec></TimeSegment>"+
			"</RecordSchedule><StreamNumber>0</StreamNumber></VideoRecordPlan>"), devAddr)
	ok, _ := readSnapMsg(t, platConn)
	if ok.StatusCode != 200 {
		t.Fatalf("status = %d", ok.StatusCode)
	}
	resp, _ := readSnapMsg(t, platConn)
	if !strings.Contains(resp.Body, "<Result>OK</Result>") {
		t.Fatalf("body = %q", resp.Body)
	}
	waitFor(t, 5*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return got != nil
	})
	mu.Lock()
	defer mu.Unlock()
	if got.RecordEnable != 1 || got.ScheduleSumNum != 2 || got.StreamNumber != 0 || len(got.Schedules) != 2 {
		t.Fatalf("decoded = %+v", got)
	}
	mon := got.Schedules[0]
	if mon.WeekDayNum != 1 || len(mon.TimeSegments) != 1 ||
		mon.TimeSegments[0].StopHour != 12 {
		t.Fatalf("monday = %+v", mon)
	}
	sun := got.Schedules[1]
	if len(sun.TimeSegments) != 2 || sun.TimeSegments[1].StopSec != 59 {
		t.Fatalf("sunday = %+v", sun)
	}
}

// TestDeviceConfigVideoAlarmRecord pins A.2.1.16: optional pre/record
// times decode as pointers.
func TestDeviceConfigVideoAlarmRecord(t *testing.T) {
	var mu sync.Mutex
	var got *manscdp.VideoAlarmRecordCmd
	platConn, devAddr := startWireTestServer(t, func(srv *device.Server) {
		srv.SetConfigHandlers(device.ConfigCallbacks{
			OnVideoAlarmRecord: func(p manscdp.VideoAlarmRecordCmd) {
				mu.Lock()
				defer mu.Unlock()
				c := p
				got = &c
			},
		})
	})

	writeSnapMsg(t, platConn, cfgMessage("var-1",
		"<VideoAlarmRecord><RecordEnable>1</RecordEnable><RecordTime>30</RecordTime>"+
			"<PreRecordTime>10</PreRecordTime><StreamNumber>1</StreamNumber></VideoAlarmRecord>"), devAddr)
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
	defer mu.Unlock()
	if got.RecordEnable != 1 || got.StreamNumber != 1 ||
		got.RecordTime == nil || *got.RecordTime != 30 ||
		got.PreRecordTime == nil || *got.PreRecordTime != 10 {
		t.Fatalf("decoded = %+v", got)
	}
}

// TestDeviceConfigPictureMask pins A.2.1.17: the RegionList wrapper
// carries a Num attribute; regions are Seq + "lx,ly,rx,ry" points.
func TestDeviceConfigPictureMask(t *testing.T) {
	var mu sync.Mutex
	var got *manscdp.PictureMaskCmd
	platConn, devAddr := startWireTestServer(t, func(srv *device.Server) {
		srv.SetConfigHandlers(device.ConfigCallbacks{
			OnPictureMask: func(m manscdp.PictureMaskCmd) {
				mu.Lock()
				defer mu.Unlock()
				c := m
				got = &c
			},
		})
	})

	writeSnapMsg(t, platConn, cfgMessage("pm-1",
		"<PictureMask><On>1</On><SumNum>2</SumNum>"+
			"<RegionList Num=\"2\">"+
			"<Item><Seq>1</Seq><Point>20,30,50,60</Point></Item>"+
			"<Item><Seq>2</Seq><Point>100,200,300,400</Point></Item>"+
			"</RegionList></PictureMask>"), devAddr)
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
	on, sumNum, regionList := got.On, got.SumNum, got.RegionList
	mu.Unlock()
	if on != 1 || sumNum != 2 || regionList == nil || len(regionList.Items) != 2 {
		t.Fatalf("decoded = %+v", got)
	}
	if regionList.Items[0].Seq != 1 || regionList.Items[0].Point != "20,30,50,60" {
		t.Fatalf("region[0] = %+v", regionList.Items[0])
	}

	// Masking off carries no RegionList at all.
	writeSnapMsg(t, platConn, cfgMessage("pm-2",
		"<PictureMask><On>0</On><SumNum>0</SumNum></PictureMask>"), devAddr)
	waitFor(t, 5*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return got.On == 0 && got.RegionList == nil
	})
}

// TestDeviceConfigOSD pins A.2.1.12: window box + time/text switches +
// up to 8 text items.
func TestDeviceConfigOSD(t *testing.T) {
	var mu sync.Mutex
	var got *manscdp.OSDConfigCmd
	platConn, devAddr := startWireTestServer(t, func(srv *device.Server) {
		srv.SetConfigHandlers(device.ConfigCallbacks{
			OnOSDConfig: func(o manscdp.OSDConfigCmd) {
				mu.Lock()
				defer mu.Unlock()
				c := o
				got = &c
			},
		})
	})

	writeSnapMsg(t, platConn, cfgMessage("osd-1",
		"<OSDConfig><Length>1920</Length><Width>1080</Width>"+
			"<TimeX>10</TimeX><TimeY>10</TimeY><TimeEnable>1</TimeEnable><TimeType>0</TimeType>"+
			"<TextEnable>1</TextEnable><SumNum>2</SumNum>"+
			"<Item><Text>Front door</Text><X>100</X><Y>200</Y></Item>"+
			"<Item><Text>Zone B</Text><X>300</X><Y>400</Y></Item>"+
			"</OSDConfig>"), devAddr)
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
	defer mu.Unlock()
	if got.Length != 1920 || got.Width != 1080 || got.TimeX != 10 || got.TimeY != 10 ||
		got.TimeEnable == nil || *got.TimeEnable != 1 || got.TimeType == nil || *got.TimeType != 0 ||
		got.SumNum != 2 || len(got.Items) != 2 {
		t.Fatalf("decoded = %+v", got)
	}
	if got.Items[0].Text != "Front door" || got.Items[1].Y != 400 {
		t.Fatalf("items = %+v", got.Items)
	}
}

// The five new sub-commands keep the explicit reject without callbacks.
func TestDeviceConfigNewSubcommandsWithoutHandlerAreRejected(t *testing.T) {
	platConn, devAddr := startWireTestServer(t, func(*device.Server) {})

	for _, sub := range []string{
		"<VideoParamAttribute><Item><StreamNumber>0</StreamNumber><VideoFormat>H.264</VideoFormat>" +
			"<Resolution>1920x1080</Resolution><FrameRate>25</FrameRate><BitRateType>0</BitRateType></Item></VideoParamAttribute>",
		"<VideoRecordPlan><RecordEnable>0</RecordEnable><RecordScheduleSumNum>0</RecordScheduleSumNum>" +
			"<StreamNumber>0</StreamNumber></VideoRecordPlan>",
		"<VideoAlarmRecord><RecordEnable>1</RecordEnable><StreamNumber>0</StreamNumber></VideoAlarmRecord>",
		"<PictureMask><On>0</On><SumNum>0</SumNum></PictureMask>",
		"<OSDConfig><Length>1</Length><Width>1</Width><TimeX>0</TimeX><TimeY>0</TimeY><SumNum>0</SumNum></OSDConfig>",
	} {
		writeSnapMsg(t, platConn, cfgMessage("rej", sub), devAddr)
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
}

// ConfigDownload (A.2.4.7) answers the VideoParamOpt block (A.2.1.20)
// from the configured download speeds / resolutions; unconfigured
// omits the block.
func TestConfigDownloadVideoParamOpt(t *testing.T) {
	platConn, devAddr := startWireTestServer(t, func(*device.Server) {})

	send := func(configType string) string {
		body := "<?xml version=\"1.0\"?><Query><CmdType>ConfigDownload</CmdType><SN>75</SN>" +
			"<DeviceID>34020000001320000001</DeviceID><ConfigType>" + configType + "</ConfigType></Query>"
		writeSnapMsg(t, platConn, device.SipMessage{
			Method:      "MESSAGE",
			RequestURI:  "sip:34020000001320000001@3402000000",
			From:        "<sip:34020000002000000001@3402000000>;tag=platvpo",
			To:          "<sip:34020000001320000001@3402000000>",
			CallID:      "vpo-" + configType,
			CSeq:        "1 MESSAGE",
			Via:         "SIP/2.0/UDP 127.0.0.1:5060;branch=z9hG4bKvpo",
			ContentType: "Application/MANSCDP+xml",
			Body:        body,
			UserAgent:   "fakeplatform",
			Headers:     map[string]string{},
		}, devAddr)
		ok, _ := readSnapMsg(t, platConn)
		if ok.StatusCode != 200 {
			t.Fatalf("status = %d", ok.StatusCode)
		}
		resp, _ := readSnapMsg(t, platConn)
		return resp.Body
	}

	// Unconfigured: OK without the block.
	bare := send("VideoParamOpt")
	if !strings.Contains(bare, "<Result>OK</Result>") || strings.Contains(bare, "<VideoParamOpt>") {
		t.Fatalf("bare body = %q", bare)
	}
}

// The configured VideoParamOpt path, pinned on the builder directly.
func TestBuildConfigDownloadVideoParamOptBlock(t *testing.T) {
	msg := device.BuildConfigDownloadResponseMessage("9", "34020000001320000001", nil,
		&device.VideoParamOptCfg{DownloadSpeed: "1/2/4", Resolution: "1920x1080/640x480"})
	want := `<Response CmdType="ConfigDownload" SN="9"><DeviceID>34020000001320000001</DeviceID>` +
		`<Result>OK</Result><VideoParamOpt><DownloadSpeed>1/2/4</DownloadSpeed>` +
		`<Resolution>1920x1080/640x480</Resolution></VideoParamOpt></Response>`
	if msg.Body != want {
		t.Fatalf("body = %q\nwant %q", msg.Body, want)
	}

	// Only one of the two fields still answers a valid block.
	partial := device.BuildConfigDownloadResponseMessage("9", "d", nil,
		&device.VideoParamOptCfg{DownloadSpeed: "1/4"})
	if !strings.Contains(partial.Body, "<DownloadSpeed>1/4</DownloadSpeed>") ||
		strings.Contains(partial.Body, "<Resolution>") {
		t.Fatalf("partial body = %q", partial.Body)
	}
}
