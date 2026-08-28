// Package gb28181 implements MANSCDP (XML body) command handling —
// device catalog, keepalive, and control command XML between the
// device and the GB/T 28181 platform.
package device

import (
	"encoding/xml"
	"fmt"
	"log/slog"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// RecordActive reports whether the device is currently recording.
// It is a package-level hook set by the recording subsystem; when nil
// or false, DeviceStatus reports <Record>OFF</Record>.
var RecordActive func() bool

// Query represents a MANSCDP Query request.
type Query struct {
	XMLName    xml.Name `xml:"Query"`
	CmdType    string   `xml:"CmdType,attr"`
	SN         string   `xml:"SN,attr"`
	DeviceID   string   `xml:"DeviceID"`
	StartTime  string   `xml:"StartTime"`
	EndTime    string   `xml:"EndTime"`
	Type       string   `xml:"Type"`
	StreamType string   `xml:"StreamType"`
}

// QueryElem represents a MANSCDP Query request with child-element format (for NVR compatibility).
type QueryElem struct {
	XMLName    xml.Name `xml:"Query"`
	CmdType    string   `xml:"CmdType"`
	SN         string   `xml:"SN"`
	DeviceID   string   `xml:"DeviceID"`
	StartTime  string   `xml:"StartTime"`
	EndTime    string   `xml:"EndTime"`
	Type       string   `xml:"Type"`
	StreamType string   `xml:"StreamType"`
}

// Response represents a MANSCDP Response message.
type Response struct {
	XMLName    xml.Name    `xml:"Response"`
	CmdType    string      `xml:"CmdType,attr"`
	SN         string      `xml:"SN,attr"`
	DeviceID   string      `xml:"DeviceID"`
	SumNum     *int        `xml:"SumNum,omitempty"`
	DeviceList *DeviceList `xml:"DeviceList,omitempty"`
	Device     *DeviceItem `xml:"Device,omitempty"`
}

// DeviceList contains a list of device/channel items.
type DeviceList struct {
	XMLName xml.Name      `xml:"DeviceList"`
	Item    []ChannelItem `xml:"Item"`
}

// ChannelItem represents a device channel catalog entry.
// ALL mandatory fields per GB/T 28181-2022 Annex A.2.1
type ChannelItem struct {
	DeviceID     string  `xml:"DeviceID"`
	Name         string  `xml:"Name"`
	Manufacturer string  `xml:"Manufacturer"`
	Model        string  `xml:"Model"`
	Owner        string  `xml:"Owner"`
	CivilCode    string  `xml:"CivilCode"`
	Address      string  `xml:"Address"`
	Parental     int     `xml:"Parental"`
	ParentID     string  `xml:"ParentID"`
	SafetyWay    int     `xml:"SafetyWay"`
	RegisterWay  int     `xml:"RegisterWay"`
	Secrecy      int     `xml:"Secrecy"`
	Status       string  `xml:"Status"`
	IPAddress    string  `xml:"IPAddress"`
	Port         int     `xml:"Port"`
	Longitude    float64 `xml:"Longitude"`
	Latitude     float64 `xml:"Latitude"`
}

// DeviceItem represents basic device information.
type DeviceItem struct {
	DeviceID     string `xml:"DeviceID"`
	Name         string `xml:"Name"`
	Manufacturer string `xml:"Manufacturer"`
	Model        string `xml:"Model"`
	Firmware     string `xml:"Firmware"`
}

// Notify represents a MANSCDP Notify message.
type Notify struct {
	XMLName  xml.Name `xml:"Notify"`
	CmdType  string   `xml:"CmdType,attr"`
	SN       string   `xml:"SN,attr"`
	DeviceID string   `xml:"DeviceID"`
	Status   string   `xml:"Status,omitempty"`
}

// NotifyElem represents a MANSCDP Notify message with child-element format (for NVR compatibility).
type NotifyElem struct {
	XMLName  xml.Name `xml:"Notify"`
	CmdType  string   `xml:"CmdType"`
	SN       string   `xml:"SN"`
	DeviceID string   `xml:"DeviceID"`
	Status   string   `xml:"Status,omitempty"`
}

// parseQueryDual tries to parse a Query body in both attribute and child-element format.
// Returns the normalized Query struct and a bool indicating success.
func parseQueryDual(body string) (Query, bool) {
	// Try attribute format first (existing struct)
	var query Query
	if err := xml.Unmarshal([]byte(body), &query); err == nil && query.CmdType != "" {
		return query, true
	}
	// Try child-element format
	var queryElem QueryElem
	if err := xml.Unmarshal([]byte(body), &queryElem); err == nil && queryElem.CmdType != "" {
		// Normalize to Query struct
		return Query{
			CmdType:    queryElem.CmdType,
			SN:         queryElem.SN,
			DeviceID:   queryElem.DeviceID,
			StartTime:  queryElem.StartTime,
			EndTime:    queryElem.EndTime,
			Type:       queryElem.Type,
			StreamType: queryElem.StreamType,
		}, true
	}
	return Query{}, false
}

// parseNotifyDual tries to parse a Notify body in both attribute and child-element format.
// Returns the normalized Notify struct and a bool indicating success.
func parseNotifyDual(body string) (Notify, bool) {
	// Try attribute format first (existing struct)
	var notify Notify
	if err := xml.Unmarshal([]byte(body), &notify); err == nil && notify.CmdType != "" {
		return notify, true
	}
	// Try child-element format
	var notifyElem NotifyElem
	if err := xml.Unmarshal([]byte(body), &notifyElem); err == nil && notifyElem.CmdType != "" {
		// Normalize to Notify struct
		return Notify{
			CmdType:  notifyElem.CmdType,
			SN:       notifyElem.SN,
			DeviceID: notifyElem.DeviceID,
			Status:   notifyElem.Status,
		}, true
	}
	return Notify{}, false
}

// PlaybackControl is a parsed SIP INFO PlaybackControl command body.
// It carries the control value (PAUSE/PLAY) plus optional seek and speed
// fields. StartTime/EndTime are unix milliseconds; Speed is a pacing
// multiplier (nil = unchanged).
type PlaybackControl struct {
	Value     string
	StartTime *int64
	EndTime   *int64
	Speed     *float64
}

// ControlElem mirrors the child-element Control body format used by
// GB/T 28181 PlaybackControl INFO messages.
type ControlElem struct {
	XMLName  xml.Name `xml:"Control"`
	CmdType  string   `xml:"CmdType"`
	SN       string   `xml:"SN"`
	DeviceID string   `xml:"DeviceID"`
	Info     struct {
		ControlValue string `xml:"ControlValue"`
		StartTime    string `xml:"StartTime"`
		EndTime      string `xml:"EndTime"`
		Speed        string `xml:"Speed"`
		Scale        string `xml:"Scale"`
	} `xml:"Info"`
}

// parsePlaybackControl parses a PlaybackControl INFO body. It is lenient:
// missing or malformed fields are tolerated (nil pointers), and the speed
// may come from either <Speed> or <Scale>. Returns ok=false when the body
// is not a PlaybackControl command.
func parsePlaybackControl(body string) (PlaybackControl, bool) {
	var ctl ControlElem
	if err := xml.Unmarshal([]byte(body), &ctl); err != nil || ctl.CmdType != "PlaybackControl" {
		return PlaybackControl{}, false
	}
	pc := PlaybackControl{Value: ctl.Info.ControlValue}
	if st, ok := parseGBTime(ctl.Info.StartTime); ok {
		pc.StartTime = &st
	}
	if et, ok := parseGBTime(ctl.Info.EndTime); ok {
		pc.EndTime = &et
	}
	// Speed may be <Speed> or <Scale>; take whichever parses.
	for _, raw := range []string{ctl.Info.Speed, ctl.Info.Scale} {
		if raw == "" {
			continue
		}
		if v, err := strconv.ParseFloat(strings.TrimSpace(raw), 64); err == nil && v > 0 {
			pc.Speed = &v
			break
		}
	}
	return pc, true
}

// DeviceContext provides device identity and network context for MANSCDP responses.
type DeviceContext struct {
	DeviceID     string
	ChannelID    string
	Name         string
	Manufacturer string
	Model        string
	Firmware     string
	LocalIP      string
	LocalPort    int
}

// buildChannel creates a ChannelItem from DeviceContext.
func buildChannel(dev DeviceContext) ChannelItem {
	return ChannelItem{
		DeviceID:     dev.ChannelID,
		Name:         dev.ChannelID,
		Status:       "ON",
		Manufacturer: dev.Manufacturer,
		Model:        dev.Model,
		Owner:        dev.DeviceID,
		ParentID:     dev.DeviceID,
		Secrecy:      0,
		RegisterWay:  1, // platform register
		IPAddress:    dev.LocalIP,
		Port:         dev.LocalPort,
	}
}

// BuildCatalogResponseMessage creates a SIP MESSAGE with Catalog response.
func BuildCatalogResponseMessage(sn, deviceID string, items []ChannelItem) SipMessage {
	sumNum := len(items)
	resp := Response{
		CmdType:  "Catalog",
		SN:       sn,
		DeviceID: deviceID,
		SumNum:   &sumNum,
		DeviceList: &DeviceList{
			Item: items,
		},
	}
	xmlData, err := xml.Marshal(resp)
	if err != nil {
		// This is a programming error — log and return empty message
		slog.Error("Failed to marshal Catalog response", "error", err)
		return SipMessage{}
	}
	return SipMessage{
		Method:      "MESSAGE",
		ContentType: "Application/MANSCDP+xml",
		Body:        string(xmlData),
		UserAgent:   "MiBee-GB28181/1.0",
		Headers:     make(map[string]string),
	}
}

// BuildDeviceInfoResponseMessage creates a SIP MESSAGE with DeviceInfo response.
func BuildDeviceInfoResponseMessage(sn, deviceID string, info DeviceItem) SipMessage {
	resp := Response{
		CmdType:  "DeviceInfo",
		SN:       sn,
		DeviceID: deviceID,
		Device:   &info,
	}
	xmlData, err := xml.Marshal(resp)
	if err != nil {
		slog.Error("Failed to marshal DeviceInfo response", "error", err)
		return SipMessage{}
	}
	return SipMessage{
		Method:      "MESSAGE",
		ContentType: "Application/MANSCDP+xml",
		Body:        string(xmlData),
		UserAgent:   "MiBee-GB28181/1.0",
		Headers:     make(map[string]string),
	}
}

// BuildKeepaliveMessage creates a SIP MESSAGE with Keepalive notify.
func BuildKeepaliveMessage(sn, deviceID, status string) SipMessage {
	if status == "" {
		status = "OK"
	}
	notify := Notify{
		CmdType:  "Keepalive",
		SN:       sn,
		DeviceID: deviceID,
		Status:   status,
	}
	xmlData, err := xml.Marshal(notify)
	if err != nil {
		slog.Error("Failed to marshal Keepalive message", "error", err)
		return SipMessage{}
	}
	return SipMessage{
		Method:      "MESSAGE",
		ContentType: "Application/MANSCDP+xml",
		Body:        string(xmlData),
		UserAgent:   "MiBee-GB28181/1.0",
		Headers:     make(map[string]string),
	}
}

// RecordItem represents one recorded segment in a RecordInfo response.
type RecordItem struct {
	DeviceID  string `xml:"DeviceID"`
	Name      string `xml:"Name"`
	FilePath  string `xml:"FilePath"`
	Address   string `xml:"Address"`
	StartTime string `xml:"StartTime"`
	EndTime   string `xml:"EndTime"`
	Secrecy   string `xml:"Secrecy"`
	Type      string `xml:"Type"`
}

// RecordInfoResponse is the MANSCDP RecordInfo response body.
type RecordInfoResponse struct {
	XMLName    xml.Name `xml:"Response"`
	CmdType    string   `xml:"CmdType,attr"`
	SN         string   `xml:"SN,attr"`
	DeviceID   string   `xml:"DeviceID"`
	Name       string   `xml:"Name"`
	SumNum     *int     `xml:"SumNum"`
	RecordList struct {
		Num  int          `xml:"Num,attr"`
		Item []RecordItem `xml:"Item"`
	} `xml:"RecordList"`
}

// BuildRecordInfoResponseMessage creates a SIP MESSAGE with a RecordInfo response.
// With no items it emits the byte-stable empty response (SumNum=0, RecordList Num=0).
func BuildRecordInfoResponseMessage(sn, deviceID string, items []RecordItem) SipMessage {
	sumNum := len(items)
	resp := RecordInfoResponse{
		CmdType:  "RecordInfo",
		SN:       sn,
		DeviceID: deviceID,
		Name:     "RecordInfo",
		SumNum:   &sumNum,
		RecordList: struct {
			Num  int          `xml:"Num,attr"`
			Item []RecordItem `xml:"Item"`
		}{
			Num:  len(items),
			Item: items,
		},
	}
	xmlData, err := xml.Marshal(resp)
	if err != nil {
		slog.Error("Failed to marshal RecordInfo response", "error", err)
		return SipMessage{}
	}
	return SipMessage{
		Method:      "MESSAGE",
		ContentType: "Application/MANSCDP+xml",
		Body:        string(xmlData),
		UserAgent:   "MiBee-GB28181/1.0",
		Headers:     make(map[string]string),
	}
}

// parseGBTime parses a GB/T 28181 timestamp (2006-01-02T15:04:05) into unix
// milliseconds in the device's local timezone, matching how recordings are
// timestamped (formatGBTime round-trips). It is lenient about a trailing
// 'Z' or timezone offset, and returns ok=false for empty or unparseable input.
func parseGBTime(s string) (int64, bool) {
	s = strings.TrimSpace(s)
	if len(s) < 19 {
		return 0, false
	}
	t, err := time.ParseInLocation("2006-01-02T15:04:05", s[:19], time.Local)
	if err != nil {
		return 0, false
	}
	return t.UnixMilli(), true
}

// formatGBTime formats unix milliseconds as a GB/T 28181 timestamp.
func formatGBTime(ms int64) string {
	return time.UnixMilli(ms).Format("2006-01-02T15:04:05")
}

// BuildDeviceStatusResponseMessage creates a SIP MESSAGE with DeviceStatus response.
func BuildDeviceStatusResponseMessage(sn, deviceID string) SipMessage {
	record := "OFF"
	if RecordActive != nil && RecordActive() {
		record = "ON"
	}
	body := fmt.Sprintf(`<Response CmdType="DeviceStatus" SN="%s"><DeviceID>%s</DeviceID><Result>OK</Result><Online>ONLINE</Online><Status>OK</Status><Encode>ON</Encode><Record>%s</Record><DeviceTime>%s</DeviceTime></Response>`, sn, deviceID, record, time.Now().Format("2006-01-02T15:04:05"))
	return SipMessage{
		Method:      "MESSAGE",
		ContentType: "Application/MANSCDP+xml",
		Body:        body,
		UserAgent:   "MiBee-GB28181/1.0",
		Headers:     make(map[string]string),
	}
}

// BuildControlRejectResponseMessage creates a SIP MESSAGE with control rejection response.
func BuildControlRejectResponseMessage(cmdType, sn, deviceID string) SipMessage {
	body := fmt.Sprintf(`<Response CmdType="%s" SN="%s"><DeviceID>%s</DeviceID><Result>ERROR</Result></Response>`, cmdType, sn, deviceID)
	return SipMessage{
		Method:      "MESSAGE",
		ContentType: "Application/MANSCDP+xml",
		Body:        body,
		UserAgent:   "MiBee-GB28181/1.0",
		Headers:     make(map[string]string),
	}
}

// RecordingIndex provides range lookup over recorded segments for RecordInfo
// queries. It is satisfied by the recording subsystem's index via an adapter
// wired in main.go, keeping gb28181 decoupled from the concrete index type.
type RecordingIndex interface {
	Lookup(startMs, endMs int64) []SegmentMeta
}

// DispatchInboundMessage parses inbound MANSCDP XML and returns appropriate responses.
// idx supplies recorded segments for RecordInfo queries; when nil, RecordInfo
// returns the empty response.
// Returns (ok200, queued_response_or_nil, error).
func DispatchInboundMessage(msg SipMessage, dev DeviceContext, idx RecordingIndex) (SipMessage, *SipMessage, error) {
	if msg.Body == "" {
		// No body to parse — just acknowledge
		return SipMessage{}, nil, nil
	}

	// Try to parse as Query (Catalog, DeviceInfo, RecordInfo, DeviceStatus, Control commands)
	if query, ok := parseQueryDual(msg.Body); ok {
		switch query.CmdType {
		case "Catalog":
			// Return 200 OK + queue Catalog response with real channel data
			ok200 := Build200OK(msg, "", "")
			channel := buildChannel(dev)
			catalogResp := BuildCatalogResponseMessage(query.SN, query.DeviceID, []ChannelItem{channel})
			return ok200, &catalogResp, nil
		case "DeviceInfo":
			// Return 200 OK + queue DeviceInfo response from dev context
			ok200 := Build200OK(msg, "", "")
			deviceInfo := DeviceItem{
				DeviceID:     dev.DeviceID,
				Name:         dev.Name,
				Manufacturer: dev.Manufacturer,
				Model:        dev.Model,
				Firmware:     dev.Firmware,
			}
			deviceInfoResp := BuildDeviceInfoResponseMessage(query.SN, query.DeviceID, deviceInfo)
			return ok200, &deviceInfoResp, nil
		case "RecordInfo":
			// Return 200 OK + queue RecordInfo response from the recording index.
			ok200 := Build200OK(msg, "", "")
			var items []RecordItem
			if idx != nil {
				startMs, hasStart := parseGBTime(query.StartTime)
				endMs, hasEnd := parseGBTime(query.EndTime)
				if hasStart && hasEnd && startMs <= endMs {
					for _, seg := range idx.Lookup(startMs, endMs) {
						items = append(items, RecordItem{
							DeviceID:  query.DeviceID,
							Name:      filepath.Base(seg.File),
							FilePath:  seg.File,
							Address:   query.DeviceID,
							StartTime: formatGBTime(seg.StartMS),
							EndTime:   formatGBTime(seg.EndMS),
							Secrecy:   "0",
							Type:      "time",
						})
					}
				}
			}
			recordInfoResp := BuildRecordInfoResponseMessage(query.SN, query.DeviceID, items)
			return ok200, &recordInfoResp, nil
		case "DeviceStatus":
			// Return 200 OK + queue DeviceStatus response
			ok200 := Build200OK(msg, "", "")
			deviceStatusResp := BuildDeviceStatusResponseMessage(query.SN, query.DeviceID)
			return ok200, &deviceStatusResp, nil
		case "DeviceControl", "Broadcast", "DeviceConfig", "HomePosition":
			// Return 200 OK + queue ControlReject response (control not supported)
			slog.Warn("Control command not supported", "cmdtype", query.CmdType)
			ok200 := Build200OK(msg, "", "")
			controlReject := BuildControlRejectResponseMessage(query.CmdType, query.SN, query.DeviceID)
			return ok200, &controlReject, nil
		default:
			slog.Warn("Unknown Query CmdType", "cmdtype", query.CmdType)
			ok200 := Build200OK(msg, "", "")
			return ok200, nil, nil
		}
	}

	// Try to parse as Notify (Keepalive ack from platform)
	if notify, ok := parseNotifyDual(msg.Body); ok {
		switch notify.CmdType {
		case "Keepalive":
			// Keepalive ack from platform — just 200 OK, no queued response
			ok200 := Build200OK(msg, "", "")
			return ok200, nil, nil
		default:
			slog.Warn("Unknown Notify CmdType", "cmdtype", notify.CmdType)
			ok200 := Build200OK(msg, "", "")
			return ok200, nil, nil
		}
	}

	// Unknown/unparseable body — log warning and return 200 OK (graceful degradation)
	slog.Warn("Failed to parse MANSCDP XML body", "body", msg.Body)
	ok200 := Build200OK(msg, "", "")
	return ok200, nil, nil
}
