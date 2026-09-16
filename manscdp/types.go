// Package manscdp implements the MANSCDP (Manufacturer and System Control
// Description Protocol) XML codec used by GB/T 28181 SIP MESSAGE bodies.
//
// Each message type is an XML document distinguished by the CmdType child element:
// the platform sends Control messages (DeviceControl) while devices answer
// with Response (Catalog/DeviceInfo/DeviceStatus/RecordInfo) or Notify
// (Keepalive/Alarm) roots. Root elements follow GB/T 28181-2016 § 9.3.
package manscdp

import "encoding/xml"

// CmdType identifies a MANSCDP command (the CmdType XML element).
// GB/T 28181-2016 § 9.3 encodes CmdType/SN as child elements — the attribute
// form is rejected by real devices.
type CmdType string

const (
	CmdCatalog         CmdType = "Catalog"
	CmdKeepalive       CmdType = "Keepalive"
	CmdDeviceInfo      CmdType = "DeviceInfo"
	CmdDeviceStatus    CmdType = "DeviceStatus"
	CmdRecordInfo      CmdType = "RecordInfo"
	CmdRecordInfoQuery CmdType = "RecordInfoQuery"
	CmdDeviceControl   CmdType = "DeviceControl"
	CmdDeviceConfig    CmdType = "DeviceConfig"
	// CmdUploadSnapShotFinished is the GB/T 28181-2022 image-snapshot
	// completion notify (A.2.5.7): the device reports the uploaded image
	// IDs after a DeviceControl SnapShot command.
	CmdUploadSnapShotFinished CmdType = "UploadSnapShotFinished"
	CmdAlarm                  CmdType = "Alarm"
	CmdTimeSync               CmdType = "TimeSync"
	CmdBroadcast              CmdType = "Broadcast"
	CmdMobilePosition         CmdType = "MobilePosition"
	// GB/T 28181-2022 information queries (A.2.4.10-14): responses reuse
	// these CmdType strings under a Response root (A.2.6.12-16).
	CmdHomePositionQuery    CmdType = "HomePositionQuery"
	CmdCruiseTrackListQuery CmdType = "CruiseTrackListQuery"
	CmdCruiseTrackQuery     CmdType = "CruiseTrackQuery"
	CmdPTZPosition          CmdType = "PTZPosition"
	CmdSDCardStatus         CmdType = "SDCardStatus"
)

// Catalog is a device's response to a platform Catalog query. It lists the
// device's channels (Item entries) wrapped in a DeviceList element.
type Catalog struct {
	XMLName  xml.Name `xml:"Response"`
	CmdType  CmdType  `xml:"CmdType"`
	SN       int      `xml:"SN"`
	DeviceID string   `xml:"DeviceID"`
	SumNum   int      `xml:"SumNum"`
	Item     []Item   `xml:"DeviceList>Item"`
	// Attribute-form aliases: some devices (minimal firmwares) emit
	// CmdType/SN as XML attributes instead of child elements. normalize()
	// coalesces them into the element fields; omitempty keeps Encode
	// emitting only the spec-correct element form.
	CmdTypeAttr CmdType `xml:"CmdType,attr,omitempty"`
	SNAttr      int     `xml:"SN,attr,omitempty"`
}

func (m *Catalog) normalize() {
	if m.CmdType == "" {
		m.CmdType = m.CmdTypeAttr
	}
	if m.SN == 0 {
		m.SN = m.SNAttr
	}
}

// Item is a single channel entry in a Catalog response.
type Item struct {
	XMLName      xml.Name `xml:"Item"`
	DeviceID     string   `xml:"DeviceID"`
	Name         string   `xml:"Name"`
	ParentID     string   `xml:"ParentID"`
	Parental     int      `xml:"Parental"`
	Status       string   `xml:"Status"`
	Manufacturer string   `xml:"Manufacturer"`
	Model        string   `xml:"Model"`
	Owner        string   `xml:"Owner"`
	CivilCode    string   `xml:"CivilCode"`
	Address      string   `xml:"Address"`
	SafetyWay    int      `xml:"SafetyWay"`
	RegisterWay  int      `xml:"RegisterWay"`
	CertNum      string   `xml:"CertNum"`
	Certifiable  int      `xml:"Certifiable"`
	ErrCode      int      `xml:"ErrCode"`
	EndTime      string   `xml:"EndTime"`
	Secrecy      int      `xml:"Secrecy"`
	PTZType      int      `xml:"PTZType"`
}

// Keepalive is a device heartbeat sent periodically to the platform.
type Keepalive struct {
	XMLName  xml.Name `xml:"Notify"`
	CmdType  CmdType  `xml:"CmdType"`
	SN       int      `xml:"SN"`
	DeviceID string   `xml:"DeviceID"`
	Status   string   `xml:"Status"`
	// Attribute-form aliases (see Catalog).
	CmdTypeAttr CmdType `xml:"CmdType,attr,omitempty"`
	SNAttr      int     `xml:"SN,attr,omitempty"`
}

func (m *Keepalive) normalize() {
	if m.CmdType == "" {
		m.CmdType = m.CmdTypeAttr
	}
	if m.SN == 0 {
		m.SN = m.SNAttr
	}
}

// DeviceInfo is a device's response to a platform DeviceInfo query.
type DeviceInfo struct {
	XMLName      xml.Name `xml:"Response"`
	CmdType      CmdType  `xml:"CmdType"`
	SN           int      `xml:"SN"`
	DeviceID     string   `xml:"DeviceID"`
	DeviceName   string   `xml:"DeviceName"`
	Manufacturer string   `xml:"Manufacturer"`
	Model        string   `xml:"Model"`
	Firmware     string   `xml:"Firmware"`
	Channel      int      `xml:"Channel"`
	Result       string   `xml:"Result"`
}

// DeviceStatus is a device's response to a platform DeviceStatus query.
type DeviceStatus struct {
	XMLName  xml.Name `xml:"Response"`
	CmdType  CmdType  `xml:"CmdType"`
	SN       int      `xml:"SN"`
	DeviceID string   `xml:"DeviceID"`
	Status   string   `xml:"Status"`
	Time     string   `xml:"Time"`
	// Attribute-form aliases (see Catalog).
	CmdTypeAttr CmdType `xml:"CmdType,attr,omitempty"`
	SNAttr      int     `xml:"SN,attr,omitempty"`
}

func (m *DeviceStatus) normalize() {
	if m.CmdType == "" {
		m.CmdType = m.CmdTypeAttr
	}
	if m.SN == 0 {
		m.SN = m.SNAttr
	}
}

// RecordInfo is a device's response to a platform RecordInfo query listing
// its recorded segments within a requested time range.
type RecordInfo struct {
	XMLName    xml.Name     `xml:"Response"`
	CmdType    CmdType      `xml:"CmdType"`
	SN         int          `xml:"SN"`
	DeviceID   string       `xml:"DeviceID"`
	Name       string       `xml:"Name"`
	SumNum     int          `xml:"SumNum"`
	RecordList []RecordItem `xml:"RecordList>Item"`
	// Attribute-form aliases (see Catalog).
	CmdTypeAttr CmdType `xml:"CmdType,attr,omitempty"`
	SNAttr      int     `xml:"SN,attr,omitempty"`
}

func (m *RecordInfo) normalize() {
	if m.CmdType == "" {
		m.CmdType = m.CmdTypeAttr
	}
	if m.SN == 0 {
		m.SN = m.SNAttr
	}
}

// RecordItem is a single recorded segment in a RecordInfo response.
type RecordItem struct {
	XMLName    xml.Name `xml:"Item"`
	DeviceID   string   `xml:"DeviceID"`
	Name       string   `xml:"Name"`
	FilePath   string   `xml:"FilePath"`
	Address    string   `xml:"Address"`
	StartTime  string   `xml:"StartTime"`
	EndTime    string   `xml:"EndTime"`
	Secrecy    int      `xml:"Secrecy"`
	Type       string   `xml:"Type"`
	RecorderID string   `xml:"RecorderID"`
}

// DeviceControl is a platform-to-device Control message. PTZCmd carries the
// 8-byte GB/T 28181 § A.4 PTZ command (XML-escaped binary); the other
// management controls are expressed via their respective optional fields
// (§ 9.3.2): RecordCmd ("Record"/"StopRecord"), GuardCmd ("SetGuard"/
// "ResetGuard"), AlarmCmd ("ResetAlarm"), TeleBoot ("Boot"), HomePosition.
type DeviceControl struct {
	XMLName  xml.Name `xml:"Control"`
	CmdType  CmdType  `xml:"CmdType"`
	SN       int      `xml:"SN"`
	DeviceID string   `xml:"DeviceID"`
	PTZCmd   string   `xml:"PTZCmd,omitempty"`
	// IFrameCmd forces the next encoded frame to be an IDR (§9.3.2;
	// value "Send"). Platforms send it when starting a pull or after
	// loss — mirrors gb28181-rs #58 readings (issue #81).
	IFrameCmd string `xml:"IFrameCmd,omitempty"`
	// HomePosition is the 看守位 control (A.2.3.1.10): auto-return to a
	// preset after ResetTime seconds of inactivity; Enabled=0 disables.
	// Absent optional fields mean "keep current".
	HomePosition *HomePositionCmd `xml:"HomePosition,omitempty"`
	// DragZoomIn / DragZoomOut carry the 拉框放大/缩小 control
	// (A.2.3.1.8/9): the box the platform user drew on the playback
	// window, in window pixels. Mirrors gb28181-rs #58 readings
	// (issue #81).
	DragZoomIn  *DragZoomCmd `xml:"DragZoomIn,omitempty"`
	DragZoomOut *DragZoomCmd `xml:"DragZoomOut,omitempty"`
	TeleBoot    string       `xml:"TeleBoot,omitempty"`
	RecordCmd   string       `xml:"RecordCmd,omitempty"`
	GuardCmd    string       `xml:"GuardCmd,omitempty"`
	AlarmCmd    string       `xml:"AlarmCmd,omitempty"`
	// SnapShot carries the GB/T 28181-2022 image-snapshot command
	// (A.2.1.24): the device captures JPEGs and uploads them over HTTP,
	// then reports UploadSnapShotFinished with the same SessionID.
	SnapShot *SnapShotCmd `xml:"SnapShot,omitempty"`
	// Attribute-form aliases (see Catalog).
	CmdTypeAttr CmdType `xml:"CmdType,attr,omitempty"`
	SNAttr      int     `xml:"SN,attr,omitempty"`
}

// SnapShotCmd is the GB/T 28181-2022 image-snapshot control payload
// (A.2.1.24 snapShotCfgType).
type SnapShotCmd struct {
	// SnapNum is the number of frames to capture, 1..10; a manual
	// snapshot is 1.
	SnapNum int `xml:"SnapNum"`
	// Interval is the per-frame interval in seconds (>=1); omitted for
	// single-frame manual snapshots.
	Interval int `xml:"Interval,omitempty"`
	// UploadURL is the HTTP endpoint the device POSTs the JPEGs to.
	UploadURL string `xml:"UploadURL"`
	// SessionID correlates the upload with the completion notify; the
	// platform generates it ([A-Za-z0-9-], 32..128 bytes).
	SessionID string `xml:"SessionID"`
}

// UploadSnapShotFinished is the GB/T 28181-2022 image-snapshot completion
// notify (A.2.5.7). An empty or short SnapShotList means the capture or
// upload failed wholly or partly.
type UploadSnapShotFinished struct {
	XMLName      xml.Name `xml:"Notify"`
	CmdType      CmdType  `xml:"CmdType"`
	SN           int      `xml:"SN"`
	DeviceID     string   `xml:"DeviceID"`
	SessionID    string   `xml:"SessionID"`
	SnapShotList []string `xml:"SnapShotList>SnapShotFileID"`
}

// BuildUploadSnapShotFinished assembles the device-side completion report.
func BuildUploadSnapShotFinished(sn int, deviceID, sessionID string, fileIDs []string) UploadSnapShotFinished {
	return UploadSnapShotFinished{
		CmdType:      CmdUploadSnapShotFinished,
		SN:           sn,
		DeviceID:     deviceID,
		SessionID:    sessionID,
		SnapShotList: fileIDs,
	}
}

func (m *DeviceControl) normalize() {
	if m.CmdType == "" {
		m.CmdType = m.CmdTypeAttr
	}
	if m.SN == 0 {
		m.SN = m.SNAttr
	}
}

// Alarm is a device-initiated alarm notification sent to the platform.
type Alarm struct {
	XMLName          xml.Name `xml:"Notify"`
	CmdType          CmdType  `xml:"CmdType"`
	SN               int      `xml:"SN"`
	DeviceID         string   `xml:"DeviceID"`
	AlarmPriority    string   `xml:"AlarmPriority"`
	AlarmMethod      string   `xml:"AlarmMethod"`
	AlarmTime        string   `xml:"AlarmTime"`
	AlarmDescription string   `xml:"AlarmDescription"`
	AlarmType        string   `xml:"AlarmType"`
	// Attribute-form aliases (see Catalog).
	CmdTypeAttr CmdType `xml:"CmdType,attr,omitempty"`
	SNAttr      int     `xml:"SN,attr,omitempty"`
}

func (m *Broadcast) normalize() {
	if m.CmdType == "" {
		m.CmdType = m.CmdTypeAttr
	}
	if m.SN == 0 {
		m.SN = m.SNAttr
	}
}

// Broadcast is the voice-broadcast notification (语音广播通知,
// GB/T 28181 §9.2.2): the platform sends it as a MESSAGE Notify so the
// device prepares its audio path for the subsequent talk INVITE.
type Broadcast struct {
	XMLName xml.Name `xml:"Notify"`
	CmdType CmdType  `xml:"CmdType"`
	SN      int      `xml:"SN"`
	// SourceID is the broadcast-origin device/platform ID; TargetID is the
	// channel the broadcast addresses.
	SourceID string `xml:"SourceID"`
	TargetID string `xml:"TargetID"`
	// Attribute-form aliases (see Catalog).
	CmdTypeAttr CmdType `xml:"CmdType,attr,omitempty"`
	SNAttr      int     `xml:"SN,attr,omitempty"`
}

// BroadcastResponse is the voice-broadcast acknowledgement (语音广播
// 应答, GB/T 28181-2022 A.2.6.11): the audio output device reports
// whether it can receive the announced broadcast. Attribute-form
// CmdType/SN like the rest of the Response family.
type BroadcastResponse struct {
	XMLName  xml.Name `xml:"Response"`
	CmdType  CmdType  `xml:"CmdType,attr"`
	SN       int      `xml:"SN,attr"`
	DeviceID string   `xml:"DeviceID"`
	Result   string   `xml:"Result"` // "OK" / "ERROR"
}

// BroadcastResultOK / BroadcastResultERROR are the A.2.6.11 Result values.
const (
	BroadcastResultOK    = "OK"
	BroadcastResultERROR = "ERROR"
)

// BuildBroadcastResponse assembles the device-side acknowledgement: OK
// when the device can receive the broadcast, ERROR otherwise.
func BuildBroadcastResponse(sn int, deviceID string, ok bool) BroadcastResponse {
	result := BroadcastResultERROR
	if ok {
		result = BroadcastResultOK
	}
	return BroadcastResponse{
		CmdType:  CmdBroadcast,
		SN:       sn,
		DeviceID: deviceID,
		Result:   result,
	}
}

func (m *Alarm) normalize() {
	if m.CmdType == "" {
		m.CmdType = m.CmdTypeAttr
	}
	if m.SN == 0 {
		m.SN = m.SNAttr
	}
}

// RecordInfoQuery is a platform-to-device request for recording information
// within a specific time range.
type RecordInfoQuery struct {
	XMLName   xml.Name `xml:"Query"`
	CmdType   CmdType  `xml:"CmdType"`
	SN        int      `xml:"SN"`
	DeviceID  string   `xml:"DeviceID"`
	StartTime string   `xml:"StartTime"`
	EndTime   string   `xml:"EndTime"`
	Type      string   `xml:"Type"`
}

// CatalogQuery is a platform-to-device request for the device's channel catalog.
// The device responds with a Catalog response listing its channels.
type CatalogQuery struct {
	XMLName  xml.Name `xml:"Query"`
	CmdType  CmdType  `xml:"CmdType"`
	SN       int      `xml:"SN"`
	DeviceID string   `xml:"DeviceID"`
}

// CatalogNotify is a device-initiated catalog change notification delivered
// via SIP NOTIFY after the platform subscribes to Catalog (GB/T 28181-2016
// § 9.5.2). Same payload shape as Catalog but rooted at Notify.
type CatalogNotify struct {
	XMLName  xml.Name `xml:"Notify"`
	CmdType  CmdType  `xml:"CmdType"`
	SN       int      `xml:"SN"`
	DeviceID string   `xml:"DeviceID"`
	SumNum   int      `xml:"SumNum"`
	Item     []Item   `xml:"DeviceList>Item"`
	// Attribute-form aliases (see Catalog).
	CmdTypeAttr CmdType `xml:"CmdType,attr,omitempty"`
	SNAttr      int     `xml:"SN,attr,omitempty"`
}

func (m *CatalogNotify) normalize() {
	if m.CmdType == "" {
		m.CmdType = m.CmdTypeAttr
	}
	if m.SN == 0 {
		m.SN = m.SNAttr
	}
}

// TimeSyncQuery is a device-to-platform clock query (GB/T 28181-2016 § 9.6).
// The platform answers with a TimeSyncResponse carrying its wall clock so the
// device can correct drift — critical for RecordInfo time ranges.
type TimeSyncQuery struct {
	XMLName  xml.Name `xml:"Query"`
	CmdType  CmdType  `xml:"CmdType"`
	SN       int      `xml:"SN"`
	DeviceID string   `xml:"DeviceID"`
}

// TimeSyncResponse is the platform's answer to a TimeSyncQuery. Time is
// ISO 8601 ("2006-01-02T15:04:05").
type TimeSyncResponse struct {
	XMLName  xml.Name `xml:"Response"`
	CmdType  CmdType  `xml:"CmdType"`
	SN       int      `xml:"SN"`
	DeviceID string   `xml:"DeviceID"`
	Time     string   `xml:"Time"`
}

// MobilePosition is a device-initiated position report (GB/T 28181-2016
// § 9.5.3) delivered via SIP NOTIFY while the platform's MobilePosition
// subscription is active. Coordinates are decimal-degree strings from the
// device (Longitude may carry an "E/W" hemisphere suffix on some firmwares).
type MobilePosition struct {
	XMLName   xml.Name `xml:"Notify"`
	CmdType   CmdType  `xml:"CmdType"`
	SN        int      `xml:"SN"`
	DeviceID  string   `xml:"DeviceID"`
	Time      string   `xml:"Time"`
	Longitude string   `xml:"Longitude"`
	Latitude  string   `xml:"Latitude"`
	Speed     string   `xml:"Speed"`
	Direction string   `xml:"Direction"`
	Altitude  string   `xml:"Altitude"`
	// Attribute-form aliases (see Catalog).
	CmdTypeAttr CmdType `xml:"CmdType,attr,omitempty"`
	SNAttr      int     `xml:"SN,attr,omitempty"`
}

func (m *MobilePosition) normalize() {
	if m.CmdType == "" {
		m.CmdType = m.CmdTypeAttr
	}
	if m.SN == 0 {
		m.SN = m.SNAttr
	}
}

// Subscribe is the platform-to-device SUBSCRIBE request body (GB/T 28181-2016
// § 9.5). CmdType selects the subject (Catalog / Alarm / MobilePosition).
// The overall subscription lifetime rides the SIP Expires header; Interval is
// MobilePosition-specific (report period in seconds).
type Subscribe struct {
	XMLName   xml.Name `xml:"SUBSCRIBE"`
	CmdType   CmdType  `xml:"CmdType"`
	SN        int      `xml:"SN"`
	DeviceID  string   `xml:"DeviceID"`
	StartTime string   `xml:"StartTime,omitempty"` // Alarm subscription window
	EndTime   string   `xml:"EndTime,omitempty"`
	Interval  int      `xml:"Interval,omitempty"` // MobilePosition report period (s)
}

// HomePositionCmd is the 看守位 (home position) control body (A.2.3.1.10):
// auto-return to PresetIndex after ResetTime seconds of inactivity.
type HomePositionCmd struct {
	Enabled     uint32  `xml:"Enabled"`               // 1 = enabled, 0 = disabled (required)
	ResetTime   *uint32 `xml:"ResetTime,omitempty"`   // auto-reset interval, seconds
	PresetIndex *uint32 `xml:"PresetIndex,omitempty"` // preset to return to, 0-255
}

// DragZoomCmd is the wire form of the 拉框放大/缩小 control payload
// (A.2.3.1.8 DragZoomIn / A.2.3.1.9 DragZoomOut): the box the platform
// user drew on the playback window, in window pixels with the origin at
// the top-left corner. All six children are required by the standard;
// they decode as pointers so a missing child stays distinguishable from
// a legitimate 0 (parity with gb28181-rs #58's strict reading).
type DragZoomCmd struct {
	Length    *int `xml:"Length"`    // 播放窗口长度像素值 (window length, px)
	Width     *int `xml:"Width"`     // 播放窗口宽度像素值 (window width, px)
	MidPointX *int `xml:"MidPointX"` // 拉框中心横轴坐标像素值 (box centre X, px)
	MidPointY *int `xml:"MidPointY"` // 拉框中心纵轴坐标像素值 (box centre Y, px)
	LengthX   *int `xml:"LengthX"`   // 拉框长度像素值 (box length, px)
	LengthY   *int `xml:"LengthY"`   // 拉框宽度像素值 (box width, px)
}

// BasicParamCmd is the A.2.3.2.2 基本参数配置 body: every child optional.
type BasicParamCmd struct {
	Name              string  `xml:"Name,omitempty"`              // 设备名称
	Expiration        *uint64 `xml:"Expiration,omitempty"`        // 注册过期时间, seconds
	HeartBeatInterval *uint64 `xml:"HeartBeatInterval,omitempty"` // 心跳间隔时间, seconds
	HeartBeatCount    *uint32 `xml:"HeartBeatCount,omitempty"`    // 心跳超时次数
}

// AlarmReportCmd is the A.2.3.2.10 报警上报开关配置 body (0 off, 1 on).
type AlarmReportCmd struct {
	MotionDetection uint32 `xml:"MotionDetection"` // 移动侦测事件上报开关
	FieldDetection  uint32 `xml:"FieldDetection"`  // 区域入侵事件上报开关
}

// DeviceConfig carries the device-configuration command (GB/T 28181-2022
// §9.3.3 / A.2.3.2, issue #80). The family allows one sub-command child;
// this decodes the subset a fixed camera can act on — BasicParam,
// FrameMirror (A.2.1.22: 0 none, 1 horizontal, 2 vertical, 3 both) and
// AlarmReport. 校时 is NOT part of this family (2022 §9.10.2 does it via
// the REGISTER response's SIP Date header).
type DeviceConfig struct {
	XMLName     xml.Name        `xml:"Control"`
	CmdType     CmdType         `xml:"CmdType"`
	SN          int             `xml:"SN"`
	DeviceID    string          `xml:"DeviceID"`
	BasicParam  *BasicParamCmd  `xml:"BasicParam,omitempty"`
	FrameMirror *uint32         `xml:"FrameMirror,omitempty"`
	AlarmReport *AlarmReportCmd `xml:"AlarmReport,omitempty"`
}
