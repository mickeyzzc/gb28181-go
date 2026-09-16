package device_test

// Wire-level tests for the §9.12.1 device half (issue #84): the
// Broadcast notification's acknowledgement and the audio-capable
// INVITE-back. The full platform↔device loop is pinned by the
// conformance suite (TestLoopback_VoiceBroadcast).

import (
	"encoding/xml"
	"strings"
	"testing"
	"time"

	"github.com/mickeyzzc/gb28181-go/device"
	"github.com/mickeyzzc/gb28181-go/manscdp"
)

func broadcastNotifyMsg(callID string) device.SipMessage {
	body := "<?xml version=\"1.0\"?><Notify><CmdType>Broadcast</CmdType><SN>42</SN>" +
		"<SourceID>34020000002000000001</SourceID>" +
		"<TargetID>34020000001320000001</TargetID></Notify>"
	return device.SipMessage{
		Method:      "MESSAGE",
		RequestURI:  "sip:34020000001320000001@3402000000",
		From:        "<sip:34020000002000000001@3402000000>;tag=bcast" + callID,
		To:          "<sip:34020000001320000001@3402000000>",
		CallID:      callID,
		CSeq:        "1 MESSAGE",
		Via:         "SIP/2.0/UDP 127.0.0.1:5060;branch=z9hG4bKbcast" + callID,
		ContentType: "Application/MANSCDP+xml",
		Body:        body,
		UserAgent:   "fakeplatform",
		Headers:     map[string]string{},
	}
}

// Without an audio sink the device declines: the acknowledgement
// carries Result=ERROR and no INVITE ever leaves.
func TestBroadcastWithoutSinkDeclines(t *testing.T) {
	platConn, devAddr := startWireTestServer(t, func(s *device.Server) {
		s.SetOnBroadcast(func(sourceID, targetID string) {})
	})

	writeSnapMsg(t, platConn, broadcastNotifyMsg("bcast-decline"), devAddr)
	ok, _ := readSnapMsg(t, platConn)
	if ok.StatusCode != 200 {
		t.Fatalf("notification status = %d, want 200", ok.StatusCode)
	}
	ack, _ := readSnapMsg(t, platConn)
	if ack.Method != "MESSAGE" || !strings.Contains(ack.Body, "Broadcast") {
		t.Fatalf("expected acknowledgement MESSAGE, got %v (body %s)", ack, ack.Body)
	}
	var resp manscdp.BroadcastResponse
	if err := xml.Unmarshal([]byte(ack.Body), &resp); err != nil {
		t.Fatalf("ack body unparseable: %v (%s)", err, ack.Body)
	}
	if resp.Result != manscdp.BroadcastResultERROR || resp.SN != 42 ||
		resp.DeviceID != "34020000001320000001" {
		t.Fatalf("ack = %+v, want ERROR / SN 42 / device ID", resp)
	}
	// No INVITE may follow the decline.
	_ = platConn.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	buf := make([]byte, 2048)
	if n, _, err := platConn.ReadFromUDP(buf); err == nil {
		t.Fatalf("INVITE must not follow a declined broadcast, got %d bytes: %s",
			n, string(buf[:n]))
	}
}
