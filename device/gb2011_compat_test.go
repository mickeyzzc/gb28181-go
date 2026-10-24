package device

import (
	"strings"
	"testing"
)

// GB/T 28181-2011 device-side compatibility. A 2011-era platform is a
// strict subset of 2016/2022 on every exchange this device answers —
// TCP media, voice broadcast/intercom, IFrameCmd/DragZoom/HomePosition/
// DeviceConfig, config & preset queries, multi-MESSAGE transport,
// mobile-position/catalog subscriptions and media keepalive are all
// 2016 additions (GB/T 28181-2016 foreword, official change list vs
// GB/T 28181-2011) — so 2011 compatibility is a wire-shape claim, not
// new code. Pin the shapes a 2011 platform actually exercises so they
// cannot regress silently:
//
//   - GB2312-declared MANSCDP bodies (the 2011 signaling charset, §6.10
//     lineage) parse through the dispatch routes;
//   - catalog / DeviceInfo / DeviceStatus queries in the 2011
//     child-element form draw attribute-form Responses
//     (CmdType/SN as attributes) a 2011 parser consumes;
//   - the INVITE answer SDP is 2011-shaped: y= SSRC, PS/90000 rtpmap,
//     sendonly, and no f= media-parameter line and no a=setup /
//     a=connection (TCP media and the f= field are 2016 additions);
//   - REGISTER carries no X-GB-Ver by default (already pinned by
//     TestRegisterLifecycleXGBVerOmittedByDefault);
//   - 2011-era controls with no host handler draw the explicit ERROR
//     reject (already pinned socket-level by
//     TestDeviceControlWithoutCallbackIsRejected — the Control root
//     decodes through the charset-aware shared codec, so GB2312-declared
//     control bodies ride that route).
//
// Charset note: queries are pure ASCII in practice (CmdType/SN/DeviceID),
// so the raw-XML Query route needs no transcoding; bodies that carry
// non-ASCII text (Control/DeviceConfig/Broadcast/Subscribe) decode
// through manscdp.Decode, which honours the declared GB2312/GBK/GB18030.
func TestDispatch_GB2011QueriesGB2312Declared(t *testing.T) {
	const devID = "34020000012000000001"
	cases := []struct {
		name string
		body string
		want []string
	}{
		{
			// The 2011 wire form: child-element Query under a GB2312
			// declaration, CRLF line endings.
			"Catalog",
			"<?xml version=\"1.0\" encoding=\"GB2312\"?>\r\n" +
				"<Query>\r\n<CmdType>Catalog</CmdType><SN>101</SN>" +
				"<DeviceID>" + devID + "</DeviceID>\r\n</Query>",
			[]string{
				`CmdType="Catalog"`, `SN="101"`, "<DeviceID>" + devID + "</DeviceID>",
				"<SumNum>1</SumNum>", `<DeviceList Num="1">`, "<Status>ON</Status>",
			},
		},
		{
			"DeviceInfo",
			"<?xml version=\"1.0\" encoding=\"GB2312\"?>\r\n" +
				"<Query><CmdType>DeviceInfo</CmdType><SN>102</SN>" +
				"<DeviceID>" + devID + "</DeviceID></Query>",
			[]string{
				`CmdType="DeviceInfo"`, `SN="102"`, "<DeviceID>" + devID + "</DeviceID>",
				"<Manufacturer>", "<Model>", "<Firmware>",
			},
		},
		{
			"DeviceStatus",
			"<?xml version=\"1.0\" encoding=\"GB2312\"?>\r\n" +
				"<Query><CmdType>DeviceStatus</CmdType><SN>103</SN>" +
				"<DeviceID>" + devID + "</DeviceID></Query>",
			[]string{
				`CmdType="DeviceStatus"`, `SN="103"`, "<DeviceID>" + devID + "</DeviceID>",
				"<Result>OK</Result>", "<Online>ONLINE</Online>",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ok200, queued, err := DispatchInboundMessage(buildInboundMessage(tc.body), testDeviceContext(), nil)
			if err != nil {
				t.Fatalf("DispatchInboundMessage failed: %v", err)
			}
			if ok200.StatusCode != 200 {
				t.Errorf("expected 200 OK, got status %d", ok200.StatusCode)
			}
			if len(queued) == 0 {
				t.Fatal("expected a queued response, got none")
			}
			for _, w := range tc.want {
				if !strings.Contains(queued[0].Body, w) {
					t.Errorf("response body missing %q:\n%s", w, queued[0].Body)
				}
			}
			// The 2011 parser reads the attribute form; an element-form
			// <CmdType> child would be a regression.
			if strings.Contains(queued[0].Body, "<CmdType>") {
				t.Errorf("response must carry CmdType/SN as attributes, got element form:\n%s", queued[0].Body)
			}
		})
	}
}

// A 2011 keepalive rides a child-element Notify body; the device
// acknowledges with a bare 200 and no queued MANSCDP.
func TestDispatch_GB2011KeepaliveNotifyAcked(t *testing.T) {
	body := "<?xml version=\"1.0\" encoding=\"GB2312\"?>\r\n" +
		"<Notify><CmdType>Keepalive</CmdType><SN>104</SN>" +
		"<DeviceID>34020000012000000001</DeviceID></Notify>"
	ok200, queued, err := DispatchInboundMessage(buildInboundMessage(body), testDeviceContext(), nil)
	if err != nil {
		t.Fatalf("DispatchInboundMessage failed: %v", err)
	}
	if ok200.StatusCode != 200 {
		t.Errorf("expected 200 OK, got status %d", ok200.StatusCode)
	}
	if len(queued) != 0 {
		t.Errorf("keepalive ack must not queue a MANSCDP response, got:\n%s", queued[0].Body)
	}
}

// The INVITE answer SDP must stay 2011-shaped: y= SSRC and the PS/90000
// rtpmap present, the 2016 additions (f= media parameters, TCP media's
// a=setup/a=connection) absent. TCP answers only ever appear when the
// platform's own offer asked for TCP, which a 2011 platform never does.
func TestDeviceSDPAnswer2011Shape(t *testing.T) {
	const ssrc = 1000000001
	sdp := buildDeviceSDP("34020000012000000001", "192.168.1.100", 30000, ssrc, "udp", "Play", "")
	for _, want := range []string{
		"v=0",
		"s=Play",
		"m=video 30000 RTP/AVP 96",
		"a=sendonly",
		"a=rtpmap:96 PS/90000",
		"y=1000000001",
	} {
		if !strings.Contains(sdp, want) {
			t.Errorf("2011 answer SDP missing %q:\n%s", want, sdp)
		}
	}
	for _, absent := range []string{"\nf=", "a=setup", "a=connection", "TCP/RTP/AVP"} {
		if strings.Contains(sdp, absent) {
			t.Errorf("2011 answer SDP must not carry the 2016+ extension %q:\n%s", absent, sdp)
		}
	}
}
