package device

import (
	"strings"
	"testing"
	"time"
)

// Wave-1 device-side completeness (twin of gb28181-rs PR #86): PresetQuery
// response, negotiated registration expiry, the silence predicate, and the
// honest Subscription-State lifetime.

func TestDispatch_PresetQuery(t *testing.T) {
	msg := buildInboundMessage(`<Query><CmdType>PresetQuery</CmdType><SN>33</SN><DeviceID>34020000001320000001</DeviceID></Query>`)
	ok200, queued, err := DispatchInboundMessage(msg, testDeviceContext(), nil)
	if err != nil {
		t.Fatalf("DispatchInboundMessage failed: %v", err)
	}
	if ok200.StatusCode != 200 {
		t.Errorf("expected 200 OK, got status %d", ok200.StatusCode)
	}
	if queued == nil {
		t.Fatal("expected a queued response, got nil")
	}
	for _, w := range []string{
		`<Response CmdType="PresetQuery" SN="33">`,
		"<DeviceID>34020000001320000001</DeviceID>",
		"<SumNum>0</SumNum>",
		`<PresetList Num="0">`,
	} {
		if !strings.Contains(queued.Body, w) {
			t.Errorf("queued body missing %q:\n%s", w, queued.Body)
		}
	}
}

func TestBuildPresetListResponseMessage_Golden(t *testing.T) {
	msg := BuildPresetListResponseMessage("7", "34020000001320000001", []PresetItem{
		{PresetID: "1", PresetName: "正门"},
		{PresetID: "2", PresetName: "后院"},
	})
	for _, w := range []string{
		`<Response CmdType="PresetQuery" SN="7">`,
		"<SumNum>2</SumNum>",
		`<PresetList Num="2">`,
		"<Item><PresetID>1</PresetID><PresetName>正门</PresetName></Item>",
		"<Item><PresetID>2</PresetID><PresetName>后院</PresetName></Item>",
	} {
		if !strings.Contains(msg.Body, w) {
			t.Errorf("body missing %q:\n%s", w, msg.Body)
		}
	}
}

func TestNegotiatedExpiresSecs(t *testing.T) {
	cases := []struct {
		expires, contact string
		want             int64
	}{
		{"7200", "", 7200}, // header
		{"30", "", 0},      // below the 60s floor
		{"", "<sip:34020000002000000001@3402000000>;expires=1800", 1800}, // contact fallback
		{"600", "<sip:x>;expires=9999", 600},                             // header wins
		{"", "", 0},                                                      // absent
		{"not-a-number", "", 0},                                          // garbage
		{"", "<sip:x>;expires=45", 0},                                    // contact below floor
	}
	for _, tc := range cases {
		if got := negotiatedExpiresSecs(tc.expires, tc.contact); got != tc.want {
			t.Errorf("negotiatedExpiresSecs(%q,%q) = %d, want %d", tc.expires, tc.contact, got, tc.want)
		}
	}
}

func TestPlatformSilent(t *testing.T) {
	heartbeat := 60 * time.Second
	if platformSilent(179*time.Second, heartbeat, 3) {
		t.Error("179s of 3×60s must not count as silent")
	}
	if !platformSilent(180*time.Second, heartbeat, 3) {
		t.Error("180s of 3×60s must count as silent")
	}
	if !platformSilent(10*time.Minute, heartbeat, 3) {
		t.Error("10min of 3×60s must count as silent")
	}
	// Degenerate configs clamp instead of misfiring.
	if platformSilent(0, 0, 3) {
		t.Error("zero elapsed must never be silent")
	}
	if platformSilent(5*time.Second, heartbeat, 0) {
		t.Error("timeout count 0 must never trip")
	}
}
