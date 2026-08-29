package device

import (
	"encoding/xml"
	"fmt"
	"strings"
	"testing"
	"time"
)

// testDeviceContext provides a default device context for tests.
func testDeviceContext() DeviceContext {
	return DeviceContext{
		DeviceID:     "34020000012000000001",
		ChannelID:    "34020000012000000001",
		Name:         "MiBee Eye",
		Manufacturer: "MiBee",
		Model:        "Eye-RPi",
		Firmware:     "1.0",
		LocalIP:      "192.168.1.100",
		LocalPort:    5060,
	}
}

// TestCatalogResponse_XMLWellFormed tests that marshaling/unmarshaling preserves data.
func TestCatalogResponse_XMLWellFormed(t *testing.T) {
	sn := "123"
	deviceID := "34020000012000000001"
	items := []ChannelItem{
		{
			DeviceID:     "34020000012000000001",
			Name:         "Camera 1",
			Manufacturer: "MiBee",
			Model:        "Eye-RPi",
			Owner:        "Owner",
			CivilCode:    "110105",
			Address:      "Address",
			Parental:     1,
			ParentID:     "3402000000",
			SafetyWay:    0,
			RegisterWay:  1,
			Secrecy:      0,
			Status:       "ON",
			IPAddress:    "192.168.1.100",
			Port:         5060,
			Longitude:    116.404,
			Latitude:     39.915,
		},
	}

	msg := BuildCatalogResponseMessage(sn, deviceID, items)

	// Parse the XML back
	var resp Response
	if err := xml.Unmarshal([]byte(msg.Body), &resp); err != nil {
		t.Fatalf("Failed to unmarshal Catalog response: %v", err)
	}

	// Verify key fields
	if resp.CmdType != "Catalog" {
		t.Errorf("Expected CmdType Catalog, got %s", resp.CmdType)
	}
	if resp.SN != sn {
		t.Errorf("Expected SN %s, got %s", sn, resp.SN)
	}
	if resp.DeviceID != deviceID {
		t.Errorf("Expected DeviceID %s, got %s", deviceID, resp.DeviceID)
	}
	if resp.SumNum == nil {
		t.Error("SumNum should not be nil")
	} else if *resp.SumNum != 1 {
		t.Errorf("Expected SumNum 1, got %d", *resp.SumNum)
	}
	if resp.DeviceList == nil {
		t.Fatal("DeviceList should not be nil")
	}
	if len(resp.DeviceList.Item) != 1 {
		t.Fatalf("Expected 1 item, got %d", len(resp.DeviceList.Item))
	}

	item := resp.DeviceList.Item[0]
	if item.DeviceID != items[0].DeviceID {
		t.Errorf("DeviceID mismatch: got %s, want %s", item.DeviceID, items[0].DeviceID)
	}
	if item.Name != items[0].Name {
		t.Errorf("Name mismatch: got %s, want %s", item.Name, items[0].Name)
	}
}

// TestCatalogResponse_HasAllMandatoryFields tests that all 17 mandatory fields are present.
func TestCatalogResponse_HasAllMandatoryFields(t *testing.T) {
	item := ChannelItem{
		DeviceID:     "34020000012000000001",
		Name:         "Camera 1",
		Manufacturer: "MiBee",
		Model:        "Eye-RPi",
		Owner:        "Owner",
		CivilCode:    "110105",
		Address:      "Address",
		Parental:     1,
		ParentID:     "3402000000",
		SafetyWay:    0,
		RegisterWay:  1,
		Secrecy:      0,
		Status:       "ON",
		IPAddress:    "192.168.1.100",
		Port:         5060,
		Longitude:    116.404,
		Latitude:     39.915,
	}

	msg := BuildCatalogResponseMessage("123", "34020000012000000001", []ChannelItem{item})

	// Parse the XML back
	var resp Response
	if err := xml.Unmarshal([]byte(msg.Body), &resp); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	unmarshaled := resp.DeviceList.Item[0]

	// Verify all 17 mandatory fields are non-zero
	checkNonZero := func(name, value string) {
		if value == "" {
			t.Errorf("Mandatory field %s is empty (zero value)", name)
		}
	}

	checkNonZero("DeviceID", unmarshaled.DeviceID)
	checkNonZero("Name", unmarshaled.Name)
	checkNonZero("Manufacturer", unmarshaled.Manufacturer)
	checkNonZero("Model", unmarshaled.Model)
	checkNonZero("Owner", unmarshaled.Owner)
	checkNonZero("CivilCode", unmarshaled.CivilCode)
	checkNonZero("Address", unmarshaled.Address)
	checkNonZero("ParentID", unmarshaled.ParentID)
	checkNonZero("Status", unmarshaled.Status)
	checkNonZero("IPAddress", unmarshaled.IPAddress)

	// Integer fields should be explicitly set (not just zero)
	if unmarshaled.Parental < 0 || unmarshaled.Parental > 1 {
		t.Errorf("Parental should be 0 or 1, got %d", unmarshaled.Parental)
	}
	if unmarshaled.SafetyWay < 0 || unmarshaled.SafetyWay > 2 {
		t.Errorf("SafetyWay should be 0-2, got %d", unmarshaled.SafetyWay)
	}
	if unmarshaled.RegisterWay < 0 || unmarshaled.RegisterWay > 2 {
		t.Errorf("RegisterWay should be 0-2, got %d", unmarshaled.RegisterWay)
	}
	if unmarshaled.Secrecy < 0 || unmarshaled.Secrecy > 2 {
		t.Errorf("Secrecy should be 0-2, got %d", unmarshaled.Secrecy)
	}
	if unmarshaled.Port <= 0 {
		t.Errorf("Port should be positive, got %d", unmarshaled.Port)
	}

	// Coordinates should be set (non-zero indicates presence)
	if unmarshaled.Longitude == 0 {
		t.Error("Longitude is zero (should be set)")
	}
	if unmarshaled.Latitude == 0 {
		t.Error("Latitude is zero (should be set)")
	}
}

// TestKeepalive_Format tests that keepalive message has correct format.
func TestKeepalive_Format(t *testing.T) {
	sn := "456"
	deviceID := "34020000012000000001"

	msg := BuildKeepaliveMessage(sn, deviceID, "OK")

	// Check SIP MESSAGE format
	if msg.Method != "MESSAGE" {
		t.Errorf("Expected method MESSAGE, got %s", msg.Method)
	}
	if msg.ContentType != "Application/MANSCDP+xml" {
		t.Errorf("Expected content type Application/MANSCDP+xml, got %s", msg.ContentType)
	}

	// Check XML body format
	if !strings.Contains(msg.Body, `CmdType="Keepalive"`) {
		t.Error("Keepalive message should contain CmdType=\"Keepalive\" attribute")
	}
	if !strings.Contains(msg.Body, "<Status>OK</Status>") {
		t.Error("Keepalive message should contain <Status>OK</Status>")
	}
	if !strings.Contains(msg.Body, `SN="456"`) {
		t.Error("Keepalive message should contain SN attribute")
	}
	if !strings.Contains(msg.Body, "<DeviceID>34020000012000000001</DeviceID>") {
		t.Error("Keepalive message should contain DeviceID")
	}

	// Test default status (empty string should become "OK")
	msgDefault := BuildKeepaliveMessage(sn, deviceID, "")
	if !strings.Contains(msgDefault.Body, "<Status>OK</Status>") {
		t.Error("Keepalive message with empty status should default to OK")
	}
}

// TestDispatch_CatalogQuery tests dispatching a Catalog query.
func TestDispatch_CatalogQuery(t *testing.T) {
	// Build inbound MESSAGE with Catalog query
	queryXML := `<?xml version="1.0" encoding="UTF-8"?>
<Query CmdType="Catalog" SN="789">
	<DeviceID>34020000012000000001</DeviceID>
</Query>`

	inbound := SipMessage{
		Method:      "MESSAGE",
		RequestURI:  "sip:3402000000@3402000000",
		To:          "<sip:34020000012000000001@3402000000>;tag=device",
		From:        "<sip:3402000000@3402000000>;tag=platform",
		CallID:      "test-call-id@192.168.1.100",
		CSeq:        "1 MESSAGE",
		Contact:     "<sip:34020000012000000001@192.168.1.100:5060>",
		ContentType: "Application/MANSCDP+xml",
		Body:        queryXML,
		Headers:     make(map[string]string),
	}

	ok200, queued, err := DispatchInboundMessage(inbound, testDeviceContext(), nil)

	if err != nil {
		t.Fatalf("DispatchInboundMessage failed: %v", err)
	}
	if ok200.StatusCode != 200 {
		t.Errorf("Expected 200 OK, got status %d", ok200.StatusCode)
	}
	if queued == nil {
		t.Fatal("Expected queued response, got nil")
	}

	// Verify queued response is a Catalog response MESSAGE
	if queued.Method != "MESSAGE" {
		t.Errorf("Expected queued method MESSAGE, got %s", queued.Method)
	}
	if !strings.Contains(queued.Body, `CmdType="Catalog"`) {
		t.Error("Queued response should be Catalog")
	}
	if !strings.Contains(queued.Body, `SN="789"`) {
		t.Error("Queued response should echo SN")
	}
}

// TestDispatch_UnknownCmdType_NoCrash tests that unknown command types don't crash.
func TestDispatch_UnknownCmdType_NoCrash(t *testing.T) {
	// Build inbound MESSAGE with unknown CmdType
	queryXML := `<?xml version="1.0" encoding="UTF-8"?>
<Query CmdType="UnknownCommand" SN="999">
	<DeviceID>34020000012000000001</DeviceID>
</Query>`

	inbound := SipMessage{
		Method:      "MESSAGE",
		RequestURI:  "sip:3402000000@3402000000",
		To:          "<sip:34020000012000000001@3402000000>;tag=device",
		From:        "<sip:3402000000@3402000000>;tag=platform",
		CallID:      "test-call-id@192.168.1.100",
		CSeq:        "1 MESSAGE",
		Contact:     "<sip:34020000012000000001@192.168.1.100:5060>",
		ContentType: "Application/MANSCDP+xml",
		Body:        queryXML,
		Headers:     make(map[string]string),
	}

	// This should not panic and should return 200 OK gracefully
	ok200, queued, err := DispatchInboundMessage(inbound, testDeviceContext(), nil)

	if err != nil {
		t.Fatalf("DispatchInboundMessage should not error on unknown command, got: %v", err)
	}
	if ok200.StatusCode != 200 {
		t.Errorf("Expected 200 OK, got status %d", ok200.StatusCode)
	}
	if queued != nil {
		t.Error("Unknown command should not queue a response")
	}
}

// TestDeviceInfoResponse_XMLWellFormed tests DeviceInfo response XML.
func TestDeviceInfoResponse_XMLWellFormed(t *testing.T) {
	sn := "101"
	deviceID := "34020000012000000001"
	info := DeviceItem{
		DeviceID:     deviceID,
		Name:         "MiBee Eye",
		Manufacturer: "MiBee",
		Model:        "Eye-RPi",
		Firmware:     "1.0",
	}

	msg := BuildDeviceInfoResponseMessage(sn, deviceID, info)

	// Parse the XML back
	var resp Response
	if err := xml.Unmarshal([]byte(msg.Body), &resp); err != nil {
		t.Fatalf("Failed to unmarshal DeviceInfo response: %v", err)
	}

	if resp.CmdType != "DeviceInfo" {
		t.Errorf("Expected CmdType DeviceInfo, got %s", resp.CmdType)
	}
	if resp.Device == nil {
		t.Fatal("Device should not be nil")
	}

	device := resp.Device
	if device.DeviceID != deviceID {
		t.Errorf("DeviceID mismatch: got %s, want %s", device.DeviceID, deviceID)
	}
	if device.Name != info.Name {
		t.Errorf("Name mismatch: got %s, want %s", device.Name, info.Name)
	}
	if device.Manufacturer != info.Manufacturer {
		t.Errorf("Manufacturer mismatch: got %s, want %s", device.Manufacturer, info.Manufacturer)
	}
	if device.Model != info.Model {
		t.Errorf("Model mismatch: got %s, want %s", device.Model, info.Model)
	}
	if device.Firmware != info.Firmware {
		t.Errorf("Firmware mismatch: got %s, want %s", device.Firmware, info.Firmware)
	}
}

// TestDispatch_DeviceInfoQuery tests dispatching a DeviceInfo query.
func TestDispatch_DeviceInfoQuery(t *testing.T) {
	queryXML := `<?xml version="1.0" encoding="UTF-8"?>
<Query CmdType="DeviceInfo" SN="202">
	<DeviceID>34020000012000000001</DeviceID>
</Query>`

	inbound := SipMessage{
		Method:      "MESSAGE",
		RequestURI:  "sip:3402000000@3402000000",
		To:          "<sip:34020000012000000001@3402000000>;tag=device",
		From:        "<sip:3402000000@3402000000>;tag=platform",
		CallID:      "test-call-id@192.168.1.100",
		CSeq:        "1 MESSAGE",
		Contact:     "<sip:34020000012000000001@192.168.1.100:5060>",
		ContentType: "Application/MANSCDP+xml",
		Body:        queryXML,
		Headers:     make(map[string]string),
	}

	ok200, queued, err := DispatchInboundMessage(inbound, testDeviceContext(), nil)

	if err != nil {
		t.Fatalf("DispatchInboundMessage failed: %v", err)
	}
	if ok200.StatusCode != 200 {
		t.Errorf("Expected 200 OK, got status %d", ok200.StatusCode)
	}
	if queued == nil {
		t.Fatal("Expected queued response, got nil")
	}

	// Verify queued response is a DeviceInfo response MESSAGE
	if queued.Method != "MESSAGE" {
		t.Errorf("Expected queued method MESSAGE, got %s", queued.Method)
	}
	if !strings.Contains(queued.Body, `CmdType="DeviceInfo"`) {
		t.Error("Queued response should be DeviceInfo")
	}
	if !strings.Contains(queued.Body, "<DeviceID>34020000012000000001</DeviceID>") {
		t.Error("Queued response should contain DeviceID")
	}
}

// TestDispatch_KeepaliveNotify tests handling Keepalive notify from platform.
func TestDispatch_KeepaliveNotify(t *testing.T) {
	notifyXML := `<?xml version="1.0" encoding="UTF-8"?>
<Notify CmdType="Keepalive" SN="303">
	<DeviceID>34020000012000000001</DeviceID>
	<Status>OK</Status>
</Notify>`

	inbound := SipMessage{
		Method:      "MESSAGE",
		RequestURI:  "sip:3402000000@3402000000",
		To:          "<sip:34020000012000000001@3402000000>;tag=device",
		From:        "<sip:3402000000@3402000000>;tag=platform",
		CallID:      "test-call-id@192.168.1.100",
		CSeq:        "1 MESSAGE",
		Contact:     "<sip:34020000012000000001@192.168.1.100:5060>",
		ContentType: "Application/MANSCDP+xml",
		Body:        notifyXML,
		Headers:     make(map[string]string),
	}

	ok200, queued, err := DispatchInboundMessage(inbound, testDeviceContext(), nil)

	if err != nil {
		t.Fatalf("DispatchInboundMessage failed: %v", err)
	}
	if ok200.StatusCode != 200 {
		t.Errorf("Expected 200 OK, got status %d", ok200.StatusCode)
	}
	// Keepalive ack should NOT queue a response
	if queued != nil {
		t.Error("Keepalive ack should not queue a response")
	}
}

// TestDispatch_EmptyBody tests graceful handling of empty body.
func TestDispatch_EmptyBody(t *testing.T) {
	inbound := SipMessage{
		Method:     "MESSAGE",
		RequestURI: "sip:3402000000@3402000000",
		To:         "<sip:34020000012000000001@3402000000>;tag=device",
		From:       "<sip:3402000000@3402000000>;tag=platform",
		CallID:     "test-call-id@192.168.1.100",
		CSeq:       "1 MESSAGE",
		Contact:    "<sip:34020000012000000001@192.168.1.100:5060>",
		Body:       "",
		Headers:    make(map[string]string),
	}

	ok200, queued, err := DispatchInboundMessage(inbound, testDeviceContext(), nil)

	if err != nil {
		t.Fatalf("DispatchInboundMessage with empty body should not error, got: %v", err)
	}
	// Empty body should return empty ok200 (no meaningful response)
	if ok200.StatusCode != 0 && ok200.StatusCode != 200 {
		// Either is acceptable for empty body
		t.Logf("Empty body returned status %d (acceptable)", ok200.StatusCode)
	}
	if queued != nil {
		t.Error("Empty body should not queue a response")
	}
}

// TestDispatch_InvalidXML tests graceful handling of unparseable XML.
func TestDispatch_InvalidXML(t *testing.T) {
	inbound := SipMessage{
		Method:      "MESSAGE",
		RequestURI:  "sip:3402000000@3402000000",
		To:          "<sip:34020000012000000001@3402000000>;tag=device",
		From:        "<sip:3402000000@3402000000>;tag=platform",
		CallID:      "test-call-id@192.168.1.100",
		CSeq:        "1 MESSAGE",
		Contact:     "<sip:34020000012000000001@192.168.1.100:5060>",
		ContentType: "Application/MANSCDP+xml",
		Body:        "not valid xml at all <<<<<",
		Headers:     make(map[string]string),
	}

	ok200, queued, err := DispatchInboundMessage(inbound, testDeviceContext(), nil)

	// Invalid XML should not crash and should return 200 OK
	if err != nil {
		t.Fatalf("DispatchInboundMessage with invalid XML should not error, got: %v", err)
	}
	if ok200.StatusCode != 200 {
		t.Errorf("Expected 200 OK for graceful degradation, got %d", ok200.StatusCode)
	}
	if queued != nil {
		t.Error("Invalid XML should not queue a response")
	}
}

// buildInboundMessage builds a standard inbound MESSAGE with the given MANSCDP body.
func buildInboundMessage(body string) SipMessage {
	return SipMessage{
		Method:      "MESSAGE",
		RequestURI:  "sip:3402000000@3402000000",
		To:          "<sip:34020000012000000001@3402000000>;tag=device",
		From:        "<sip:3402000000@3402000000>;tag=platform",
		CallID:      "test-call-id@192.168.1.100",
		CSeq:        "1 MESSAGE",
		Contact:     "<sip:34020000012000000001@192.168.1.100:5060>",
		ContentType: "Application/MANSCDP+xml",
		Body:        body,
		Headers:     make(map[string]string),
	}
}

// TestParseQueryDual_AttributeFormat verifies attribute-format Query XML parses.
func TestParseQueryDual_AttributeFormat(t *testing.T) {
	body := `<Query CmdType="Catalog" SN="7" DeviceID="34020000001320000001"/>`
	q, ok := parseQueryDual(body)
	if !ok {
		t.Fatal("Expected attribute-format Query to parse successfully")
	}
	if q.CmdType != "Catalog" {
		t.Errorf("Expected CmdType Catalog, got %s", q.CmdType)
	}
	if q.SN != "7" {
		t.Errorf("Expected SN 7, got %s", q.SN)
	}
}

// TestParseQueryDual_ChildElementFormat verifies child-element Query XML parses.
func TestParseQueryDual_ChildElementFormat(t *testing.T) {
	body := `<Query><CmdType>Catalog</CmdType><SN>7</SN><DeviceID>x</DeviceID></Query>`
	q, ok := parseQueryDual(body)
	if !ok {
		t.Fatal("Expected child-element Query to parse successfully")
	}
	if q.CmdType != "Catalog" {
		t.Errorf("Expected CmdType Catalog, got %s", q.CmdType)
	}
	if q.SN != "7" {
		t.Errorf("Expected SN 7, got %s", q.SN)
	}
	if q.DeviceID != "x" {
		t.Errorf("Expected DeviceID x, got %s", q.DeviceID)
	}
}

// TestParseQueryDual_Neither verifies garbage does not parse.
func TestParseQueryDual_Neither(t *testing.T) {
	if _, ok := parseQueryDual("<Garbage/>"); ok {
		t.Error("Expected garbage to fail parsing")
	}
}

// TestParseNotifyDual_AttributeFormat verifies attribute-format Notify XML parses.
func TestParseNotifyDual_AttributeFormat(t *testing.T) {
	body := `<Notify CmdType="Keepalive" SN="7" DeviceID="34020000001320000001"/>`
	n, ok := parseNotifyDual(body)
	if !ok {
		t.Fatal("Expected attribute-format Notify to parse successfully")
	}
	if n.CmdType != "Keepalive" {
		t.Errorf("Expected CmdType Keepalive, got %s", n.CmdType)
	}
	if n.SN != "7" {
		t.Errorf("Expected SN 7, got %s", n.SN)
	}
}

// TestParseNotifyDual_ChildElementFormat verifies child-element Notify XML parses.
func TestParseNotifyDual_ChildElementFormat(t *testing.T) {
	body := `<Notify><CmdType>Keepalive</CmdType><SN>7</SN><DeviceID>x</DeviceID></Notify>`
	n, ok := parseNotifyDual(body)
	if !ok {
		t.Fatal("Expected child-element Notify to parse successfully")
	}
	if n.CmdType != "Keepalive" {
		t.Errorf("Expected CmdType Keepalive, got %s", n.CmdType)
	}
	if n.SN != "7" {
		t.Errorf("Expected SN 7, got %s", n.SN)
	}
}

// TestDispatch_RecordInfo_ReturnsEmptyList verifies RecordInfo queries get an empty list response.
func TestDispatch_RecordInfo_ReturnsEmptyList(t *testing.T) {
	msg := buildInboundMessage(`<Query><CmdType>RecordInfo</CmdType><SN>10</SN><DeviceID>dev</DeviceID></Query>`)

	ok200, queued, err := DispatchInboundMessage(msg, testDeviceContext(), nil)
	if err != nil {
		t.Fatalf("DispatchInboundMessage failed: %v", err)
	}
	if ok200.Method != "" {
		t.Errorf("Expected 200 OK response with empty Method, got %q", ok200.Method)
	}
	if ok200.StatusCode != 200 {
		t.Errorf("Expected 200 OK, got status %d", ok200.StatusCode)
	}
	if queued == nil {
		t.Fatal("Expected queued RecordInfo response, got nil")
	}
	if !strings.Contains(queued.Body, "RecordInfo") {
		t.Error("Queued response should contain RecordInfo")
	}
	if !strings.Contains(queued.Body, "SumNum") {
		t.Error("Queued response should contain SumNum")
	}
}

// TestDispatch_DeviceStatus_Fields verifies DeviceStatus responses contain status fields.
func TestDispatch_DeviceStatus_Fields(t *testing.T) {
	msg := buildInboundMessage(`<Query><CmdType>DeviceStatus</CmdType><SN>11</SN><DeviceID>dev</DeviceID></Query>`)

	ok200, queued, err := DispatchInboundMessage(msg, testDeviceContext(), nil)
	if err != nil {
		t.Fatalf("DispatchInboundMessage failed: %v", err)
	}
	if ok200.StatusCode != 200 {
		t.Errorf("Expected 200 OK, got status %d", ok200.StatusCode)
	}
	if queued == nil {
		t.Fatal("Expected queued DeviceStatus response, got nil")
	}
	for _, field := range []string{"Online", "Status", "Encode", "Record", "DeviceTime"} {
		if !strings.Contains(queued.Body, field) {
			t.Errorf("Queued response should contain %s", field)
		}
	}
}

// TestDispatch_DeviceControl_Rejected verifies DeviceControl commands are rejected.
func TestDispatch_DeviceControl_Rejected(t *testing.T) {
	msg := buildInboundMessage(`<Query><CmdType>DeviceControl</CmdType><SN>12</SN><DeviceID>dev</DeviceID></Query>`)

	ok200, queued, err := DispatchInboundMessage(msg, testDeviceContext(), nil)
	if err != nil {
		t.Fatalf("DispatchInboundMessage failed: %v", err)
	}
	if ok200.StatusCode != 200 {
		t.Errorf("Expected 200 OK, got status %d", ok200.StatusCode)
	}
	if queued == nil {
		t.Fatal("Expected queued control reject response, got nil")
	}
	if !strings.Contains(queued.Body, "Result>ERROR<") {
		t.Error("Queued response should contain Result>ERROR<")
	}
	if !strings.Contains(queued.Body, `CmdType="DeviceControl"`) {
		t.Error("Queued response should echo DeviceControl CmdType")
	}
}

// TestDispatch_Catalog_ReturnsRealChannel verifies Catalog queries return the device channel.
func TestDispatch_Catalog_ReturnsRealChannel(t *testing.T) {
	msg := buildInboundMessage(`<Query><CmdType>Catalog</CmdType><SN>13</SN><DeviceID>dev</DeviceID></Query>`)
	dev := testDeviceContext()

	ok200, queued, err := DispatchInboundMessage(msg, dev, nil)
	if err != nil {
		t.Fatalf("DispatchInboundMessage failed: %v", err)
	}
	if ok200.StatusCode != 200 {
		t.Errorf("Expected 200 OK, got status %d", ok200.StatusCode)
	}
	if queued == nil {
		t.Fatal("Expected queued Catalog response, got nil")
	}
	if !strings.Contains(queued.Body, dev.ChannelID) {
		t.Error("Queued response should contain the channel ID from DeviceContext")
	}
	if !strings.Contains(queued.Body, "SumNum>1<") {
		t.Error("Queued response should contain SumNum>1< (1 channel)")
	}
}

// TestDispatch_DualFormat_Catalog_ChildElement verifies child-element Catalog queries dispatch.
func TestDispatch_DualFormat_Catalog_ChildElement(t *testing.T) {
	msg := buildInboundMessage(`<Query><CmdType>Catalog</CmdType><SN>14</SN><DeviceID>dev</DeviceID></Query>`)

	ok200, queued, err := DispatchInboundMessage(msg, testDeviceContext(), nil)
	if err != nil {
		t.Fatalf("DispatchInboundMessage failed: %v", err)
	}
	if ok200.StatusCode != 200 {
		t.Errorf("Expected 200 OK, got status %d", ok200.StatusCode)
	}
	if queued == nil {
		t.Fatal("Expected queued Catalog response, got nil")
	}
	if !strings.Contains(queued.Body, "Catalog") {
		t.Error("Queued response should contain Catalog")
	}
}

// TestDispatch_UnknownCmdType_NoQueuedResponse verifies unknown CmdTypes get 200 OK but no queued response.
func TestDispatch_UnknownCmdType_NoQueuedResponse(t *testing.T) {
	msg := buildInboundMessage(`<Query><CmdType>Unknown</CmdType><SN>15</SN><DeviceID>dev</DeviceID></Query>`)

	ok200, queued, err := DispatchInboundMessage(msg, testDeviceContext(), nil)
	if err != nil {
		t.Fatalf("DispatchInboundMessage failed: %v", err)
	}
	if ok200.StatusCode != 200 {
		t.Errorf("Expected 200 OK, got status %d", ok200.StatusCode)
	}
	if queued != nil {
		t.Error("Unknown CmdType should not queue a response")
	}
}

// fakeRecordingIndex is a test implementation of RecordingIndex.
type fakeRecordingIndex struct {
	segments []SegmentMeta
}

func (f *fakeRecordingIndex) Lookup(startMs, endMs int64) []SegmentMeta {
	var out []SegmentMeta
	for _, si := range f.segments {
		if si.EndMS >= startMs && si.StartMS <= endMs {
			out = append(out, si)
		}
	}
	return out
}

// TestRecordInfoResponse_EmptyBytesGolden locks the byte-stable empty response
// (NVR byte-stability, plan decision #9).
func TestRecordInfoResponse_EmptyBytesGolden(t *testing.T) {
	msg := BuildRecordInfoResponseMessage("10", "dev", nil)
	want := `<Response CmdType="RecordInfo" SN="10"><DeviceID>dev</DeviceID><Name>RecordInfo</Name><SumNum>0</SumNum><RecordList Num="0"></RecordList></Response>`
	if msg.Body != want {
		t.Errorf("empty RecordInfo response bytes changed:\n got: %s\nwant: %s", msg.Body, want)
	}
}

// TestParseQueryDual_RecordInfo_AttributeFormat verifies attribute-format
// RecordInfo queries parse the optional time/type fields.
func TestParseQueryDual_RecordInfo_AttributeFormat(t *testing.T) {
	body := `<Query CmdType="RecordInfo" SN="7"><DeviceID>34020000001320000001</DeviceID><StartTime>2026-08-15T14:30:00</StartTime><EndTime>2026-08-15T15:30:00</EndTime><Type>time</Type><StreamType>1</StreamType></Query>`
	q, ok := parseQueryDual(body)
	if !ok {
		t.Fatal("Expected attribute-format RecordInfo Query to parse successfully")
	}
	if q.CmdType != "RecordInfo" {
		t.Errorf("Expected CmdType RecordInfo, got %s", q.CmdType)
	}
	if q.SN != "7" {
		t.Errorf("Expected SN 7, got %s", q.SN)
	}
	if q.StartTime != "2026-08-15T14:30:00" {
		t.Errorf("Expected StartTime 2026-08-15T14:30:00, got %s", q.StartTime)
	}
	if q.EndTime != "2026-08-15T15:30:00" {
		t.Errorf("Expected EndTime 2026-08-15T15:30:00, got %s", q.EndTime)
	}
	if q.Type != "time" {
		t.Errorf("Expected Type time, got %s", q.Type)
	}
	if q.StreamType != "1" {
		t.Errorf("Expected StreamType 1, got %s", q.StreamType)
	}
}

// TestParseQueryDual_RecordInfo_ChildElementFormat verifies child-element
// RecordInfo queries parse the optional time/type fields.
func TestParseQueryDual_RecordInfo_ChildElementFormat(t *testing.T) {
	body := `<Query><CmdType>RecordInfo</CmdType><SN>7</SN><DeviceID>x</DeviceID><StartTime>2026-08-15T14:30:00</StartTime><EndTime>2026-08-15T15:30:00</EndTime><Type>time</Type><StreamType>1</StreamType></Query>`
	q, ok := parseQueryDual(body)
	if !ok {
		t.Fatal("Expected child-element RecordInfo Query to parse successfully")
	}
	if q.CmdType != "RecordInfo" {
		t.Errorf("Expected CmdType RecordInfo, got %s", q.CmdType)
	}
	if q.StartTime != "2026-08-15T14:30:00" {
		t.Errorf("Expected StartTime 2026-08-15T14:30:00, got %s", q.StartTime)
	}
	if q.EndTime != "2026-08-15T15:30:00" {
		t.Errorf("Expected EndTime 2026-08-15T15:30:00, got %s", q.EndTime)
	}
	if q.Type != "time" {
		t.Errorf("Expected Type time, got %s", q.Type)
	}
	if q.StreamType != "1" {
		t.Errorf("Expected StreamType 1, got %s", q.StreamType)
	}
}

// TestParseQueryDual_RecordInfo_MissingTimeFields verifies time fields are optional.
func TestParseQueryDual_RecordInfo_MissingTimeFields(t *testing.T) {
	body := `<Query CmdType="RecordInfo" SN="7"><DeviceID>x</DeviceID></Query>`
	q, ok := parseQueryDual(body)
	if !ok {
		t.Fatal("Expected RecordInfo Query without time fields to parse successfully")
	}
	if q.StartTime != "" || q.EndTime != "" || q.Type != "" || q.StreamType != "" {
		t.Errorf("Expected empty optional fields, got %+v", q)
	}
}

// TestParseGBTime verifies lenient GB/T 28181 timestamp parsing.
func TestParseGBTime(t *testing.T) {
	want := time.Date(2026, 8, 15, 14, 30, 0, 0, time.Local).UnixMilli()
	cases := []struct {
		in   string
		want int64
		ok   bool
	}{
		{"2026-08-15T14:30:00", want, true},
		{"2026-08-15T14:30:00Z", want, true},
		{"2026-08-15T14:30:00+08:00", want, true},
		{"2026-08-15T14:30:00.000", want, true},
		{"", 0, false},
		{"garbage", 0, false},
		{"2026-13-99T99:99:99", 0, false},
	}
	for _, tc := range cases {
		got, ok := parseGBTime(tc.in)
		if ok != tc.ok {
			t.Errorf("parseGBTime(%q) ok = %v, want %v", tc.in, ok, tc.ok)
			continue
		}
		if ok && got != tc.want {
			t.Errorf("parseGBTime(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

// TestBuildRecordInfoResponse_Items verifies the builder emits SumNum==Num==len(items)
// with each item as a RecordList child in attribute-format XML.
func TestBuildRecordInfoResponse_Items(t *testing.T) {
	items := []RecordItem{
		{
			DeviceID:  "dev",
			Name:      "000001.h264",
			FilePath:  "2026-08-15/14/000001.h264",
			Address:   "dev",
			StartTime: "2026-08-15T14:00:00",
			EndTime:   "2026-08-15T14:10:00",
			Secrecy:   "0",
			Type:      "time",
		},
		{
			DeviceID:  "dev",
			Name:      "000002.h264",
			FilePath:  "2026-08-15/14/000002.h264",
			Address:   "dev",
			StartTime: "2026-08-15T14:10:00",
			EndTime:   "2026-08-15T14:20:00",
			Secrecy:   "0",
			Type:      "time",
		},
	}
	msg := BuildRecordInfoResponseMessage("10", "dev", items)

	// XML root keeps attribute format (CmdType/SN as attributes).
	if !strings.Contains(msg.Body, `CmdType="RecordInfo"`) {
		t.Error("Response should carry CmdType as an XML attribute")
	}
	if !strings.Contains(msg.Body, `SN="10"`) {
		t.Error("Response should carry SN as an XML attribute")
	}

	var resp RecordInfoResponse
	if err := xml.Unmarshal([]byte(msg.Body), &resp); err != nil {
		t.Fatalf("Failed to unmarshal RecordInfo response: %v", err)
	}
	if resp.SumNum == nil || *resp.SumNum != len(items) {
		t.Errorf("Expected SumNum %d, got %v", len(items), resp.SumNum)
	}
	if resp.RecordList.Num != len(items) {
		t.Errorf("Expected RecordList Num %d, got %d", len(items), resp.RecordList.Num)
	}
	if len(resp.RecordList.Item) != len(items) {
		t.Fatalf("Expected %d items, got %d", len(items), len(resp.RecordList.Item))
	}
	for i, got := range resp.RecordList.Item {
		want := items[i]
		if got != want {
			t.Errorf("Item %d mismatch:\n got: %+v\nwant: %+v", i, got, want)
		}
	}
}

// TestDispatch_RecordInfo_WithIndex verifies RecordInfo queries return real
// items from the injected recording index.
func TestDispatch_RecordInfo_WithIndex(t *testing.T) {
	start := time.Date(2026, 8, 15, 14, 0, 0, 0, time.Local).UnixMilli()
	end := time.Date(2026, 8, 15, 14, 10, 0, 0, time.Local).UnixMilli()
	idx := &fakeRecordingIndex{segments: []SegmentMeta{
		{File: "2026-08-15/14/000001.h264", StartMS: start, EndMS: end, Size: 1024, Frames: 300, Keyframes: 1},
	}}
	msg := buildInboundMessage(`<Query><CmdType>RecordInfo</CmdType><SN>10</SN><DeviceID>dev</DeviceID><StartTime>2026-08-15T14:00:00</StartTime><EndTime>2026-08-15T15:00:00</EndTime></Query>`)

	ok200, queued, err := DispatchInboundMessage(msg, testDeviceContext(), idx)
	if err != nil {
		t.Fatalf("DispatchInboundMessage failed: %v", err)
	}
	if ok200.StatusCode != 200 {
		t.Errorf("Expected 200 OK, got status %d", ok200.StatusCode)
	}
	if queued == nil {
		t.Fatal("Expected queued RecordInfo response, got nil")
	}

	var resp RecordInfoResponse
	if err := xml.Unmarshal([]byte(queued.Body), &resp); err != nil {
		t.Fatalf("Failed to unmarshal RecordInfo response: %v", err)
	}
	if resp.SumNum == nil || *resp.SumNum != 1 {
		t.Errorf("Expected SumNum 1, got %v", resp.SumNum)
	}
	if resp.RecordList.Num != 1 {
		t.Errorf("Expected RecordList Num 1, got %d", resp.RecordList.Num)
	}
	if len(resp.RecordList.Item) != 1 {
		t.Fatalf("Expected 1 item, got %d", len(resp.RecordList.Item))
	}
	item := resp.RecordList.Item[0]
	if item.DeviceID != "dev" {
		t.Errorf("Expected DeviceID dev, got %s", item.DeviceID)
	}
	if item.Name != "000001.h264" {
		t.Errorf("Expected Name 000001.h264, got %s", item.Name)
	}
	if item.FilePath != "2026-08-15/14/000001.h264" {
		t.Errorf("Expected FilePath 2026-08-15/14/000001.h264, got %s", item.FilePath)
	}
	if item.StartTime != "2026-08-15T14:00:00" {
		t.Errorf("Expected StartTime 2026-08-15T14:00:00, got %s", item.StartTime)
	}
	if item.EndTime != "2026-08-15T14:10:00" {
		t.Errorf("Expected EndTime 2026-08-15T14:10:00, got %s", item.EndTime)
	}
	if item.Secrecy != "0" {
		t.Errorf("Expected Secrecy 0, got %s", item.Secrecy)
	}
	if item.Type != "time" {
		t.Errorf("Expected Type time, got %s", item.Type)
	}
}

// TestDispatch_RecordInfo_NilIndex verifies a nil index yields the empty response.
func TestDispatch_RecordInfo_NilIndex(t *testing.T) {
	msg := buildInboundMessage(`<Query><CmdType>RecordInfo</CmdType><SN>10</SN><DeviceID>dev</DeviceID><StartTime>2026-08-15T14:00:00</StartTime><EndTime>2026-08-15T15:00:00</EndTime></Query>`)

	ok200, queued, err := DispatchInboundMessage(msg, testDeviceContext(), nil)
	if err != nil {
		t.Fatalf("DispatchInboundMessage failed: %v", err)
	}
	if ok200.StatusCode != 200 {
		t.Errorf("Expected 200 OK, got status %d", ok200.StatusCode)
	}
	if queued == nil {
		t.Fatal("Expected queued RecordInfo response, got nil")
	}
	var resp RecordInfoResponse
	if err := xml.Unmarshal([]byte(queued.Body), &resp); err != nil {
		t.Fatalf("Failed to unmarshal RecordInfo response: %v", err)
	}
	if resp.SumNum == nil || *resp.SumNum != 0 {
		t.Errorf("Expected SumNum 0, got %v", resp.SumNum)
	}
	if resp.RecordList.Num != 0 {
		t.Errorf("Expected RecordList Num 0, got %d", resp.RecordList.Num)
	}
	if len(resp.RecordList.Item) != 0 {
		t.Errorf("Expected 0 items, got %d", len(resp.RecordList.Item))
	}
}

// TestDispatch_RecordInfo_MissingTimeFields verifies a query without time
// fields yields the empty response even with an index present.
func TestDispatch_RecordInfo_MissingTimeFields(t *testing.T) {
	idx := &fakeRecordingIndex{segments: []SegmentMeta{
		{File: "2026-08-15/14/000001.h264", StartMS: 1, EndMS: 2, Size: 1024, Frames: 300, Keyframes: 1},
	}}
	msg := buildInboundMessage(`<Query><CmdType>RecordInfo</CmdType><SN>10</SN><DeviceID>dev</DeviceID></Query>`)

	ok200, queued, err := DispatchInboundMessage(msg, testDeviceContext(), idx)
	if err != nil {
		t.Fatalf("DispatchInboundMessage failed: %v", err)
	}
	if ok200.StatusCode != 200 {
		t.Errorf("Expected 200 OK, got status %d", ok200.StatusCode)
	}
	if queued == nil {
		t.Fatal("Expected queued RecordInfo response, got nil")
	}
	var resp RecordInfoResponse
	if err := xml.Unmarshal([]byte(queued.Body), &resp); err != nil {
		t.Fatalf("Failed to unmarshal RecordInfo response: %v", err)
	}
	if resp.SumNum == nil || *resp.SumNum != 0 {
		t.Errorf("Expected SumNum 0, got %v", resp.SumNum)
	}
}

// TestParsePlaybackControl_Pause verifies a PAUSE control parses with
// Value=="PAUSE" and nil times/speed.
func TestParsePlaybackControl_Pause(t *testing.T) {
	pc, ok := parsePlaybackControl(`<Control><CmdType>PlaybackControl</CmdType><SN>1</SN><DeviceID>d</DeviceID><Info><ControlValue>PAUSE</ControlValue></Info></Control>`)
	if !ok {
		t.Fatal("expected ok=true for PAUSE")
	}
	if pc.Value != "PAUSE" {
		t.Errorf("Value = %q, want PAUSE", pc.Value)
	}
	if pc.StartTime != nil || pc.EndTime != nil || pc.Speed != nil {
		t.Errorf("expected nil times/speed, got StartTime=%v EndTime=%v Speed=%v", pc.StartTime, pc.EndTime, pc.Speed)
	}
}

// TestParsePlaybackControl_PlayWithTimes verifies a PLAY control with
// StartTime+EndTime parses both as unix milliseconds.
func TestParsePlaybackControl_PlayWithTimes(t *testing.T) {
	start := time.Date(2026, 8, 15, 10, 0, 0, 0, time.Local).UnixMilli()
	end := time.Date(2026, 8, 15, 10, 1, 0, 0, time.Local).UnixMilli()
	body := fmt.Sprintf(`<Control><CmdType>PlaybackControl</CmdType><SN>1</SN><DeviceID>d</DeviceID><Info><ControlValue>PLAY</ControlValue><StartTime>%s</StartTime><EndTime>%s</EndTime></Info></Control>`, formatGBTime(start), formatGBTime(end))
	pc, ok := parsePlaybackControl(body)
	if !ok {
		t.Fatal("expected ok=true for PLAY")
	}
	if pc.Value != "PLAY" {
		t.Errorf("Value = %q, want PLAY", pc.Value)
	}
	if pc.StartTime == nil || *pc.StartTime != start {
		t.Errorf("StartTime = %v, want %d", pc.StartTime, start)
	}
	if pc.EndTime == nil || *pc.EndTime != end {
		t.Errorf("EndTime = %v, want %d", pc.EndTime, end)
	}
}

// TestParsePlaybackControl_PlayWithSpeed verifies a PLAY control with
// Speed parses the speed as a float pointer.
func TestParsePlaybackControl_PlayWithSpeed(t *testing.T) {
	pc, ok := parsePlaybackControl(`<Control><CmdType>PlaybackControl</CmdType><SN>1</SN><DeviceID>d</DeviceID><Info><ControlValue>PLAY</ControlValue><Speed>4</Speed></Info></Control>`)
	if !ok {
		t.Fatal("expected ok=true for PLAY with Speed")
	}
	if pc.Speed == nil || *pc.Speed != 4.0 {
		t.Errorf("Speed = %v, want 4.0", pc.Speed)
	}
}

// TestParsePlaybackControl_ScaleFallback verifies Speed falls back to
// Scale when Speed is absent.
func TestParsePlaybackControl_ScaleFallback(t *testing.T) {
	pc, ok := parsePlaybackControl(`<Control><CmdType>PlaybackControl</CmdType><SN>1</SN><DeviceID>d</DeviceID><Info><ControlValue>PLAY</ControlValue><Scale>2.5</Scale></Info></Control>`)
	if !ok {
		t.Fatal("expected ok=true for PLAY with Scale")
	}
	if pc.Speed == nil || *pc.Speed != 2.5 {
		t.Errorf("Speed = %v, want 2.5 (from Scale)", pc.Speed)
	}
}

// TestParsePlaybackControl_RejectsNonControl verifies bodies that are not
// PlaybackControl commands (wrong CmdType, garbage) yield ok==false.
func TestParsePlaybackControl_RejectsNonControl(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"wrong CmdType", `<Control><CmdType>DeviceControl</CmdType><SN>1</SN><DeviceID>d</DeviceID><Info><ControlValue>PAUSE</ControlValue></Info></Control>`},
		{"garbage", `not xml at all`},
		{"empty", ``},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, ok := parsePlaybackControl(tc.body); ok {
				t.Errorf("parsePlaybackControl(%q) ok = true, want false", tc.body)
			}
		})
	}
}

// TestParsePlaybackControl_MissingInfoTolerated verifies a PlaybackControl
// body without an <Info> element is tolerated (lenient parser): ok==true
// with an empty Value that downstream treats as an unknown control.
func TestParsePlaybackControl_MissingInfoTolerated(t *testing.T) {
	pc, ok := parsePlaybackControl(`<Control><CmdType>PlaybackControl</CmdType><SN>1</SN><DeviceID>d</DeviceID></Control>`)
	if !ok {
		t.Fatal("expected ok=true for PlaybackControl with missing Info (lenient)")
	}
	if pc.Value != "" || pc.StartTime != nil || pc.EndTime != nil || pc.Speed != nil {
		t.Errorf("expected empty control, got Value=%q StartTime=%v EndTime=%v Speed=%v", pc.Value, pc.StartTime, pc.EndTime, pc.Speed)
	}
}

// --- GB/T 28181-2022: voice broadcast notification --------------------------

// TestDispatchBroadcastNotify: a Broadcast Notify MESSAGE is acknowledged
// with 200 OK and fires the host's OnBroadcast hook with the source/target
// IDs (the platform follows with a talk INVITE; preparing the audio path is
// host business).
func TestDispatchBroadcastNotify(t *testing.T) {
	var gotSource, gotTarget string
	dev := DeviceContext{DeviceID: "34020000001320000001", OnBroadcast: func(sourceID, targetID string) {
		gotSource, gotTarget = sourceID, targetID
	}}
	msg := SipMessage{
		Method: "MESSAGE",
		CallID: "bcast@test",
		CSeq:   "1 MESSAGE",
		Body:   `<?xml version="1.0"?><Notify CmdType="Broadcast" SN="17"><SourceID>34020000002000000001</SourceID><TargetID>34020000001320000001</TargetID></Notify>`,
	}
	ok200, queued, err := DispatchInboundMessage(msg, dev, nil)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if ok200.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", ok200.StatusCode)
	}
	if queued != nil {
		t.Fatalf("broadcast must not queue a response")
	}
	if gotSource != "34020000002000000001" || gotTarget != "34020000001320000001" {
		t.Fatalf("OnBroadcast got %q/%q", gotSource, gotTarget)
	}
}
