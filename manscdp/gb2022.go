package manscdp

import "encoding/xml"

// GB/T 28181-2022 information queries (standard foreword: "增加了看守位
// 信息查询、巡航轨迹列表查询、巡航轨迹查询、PTZ 精准状态查询、存储卡
// 状态查询"; wire shapes pinned to A.2.4.10-14 and A.2.6.12-16). Responses
// reuse the query's CmdType string under a Response root, exactly as the
// RecordInfo query/answer pair does.

// HomePositionQuery asks a device for its看守位 (home position) settings.
type HomePositionQuery struct {
	XMLName  xml.Name `xml:"Query"`
	CmdType  CmdType  `xml:"CmdType"`
	SN       int      `xml:"SN"`
	DeviceID string   `xml:"DeviceID"`
	// Attribute-form aliases (see Catalog).
	CmdTypeAttr CmdType `xml:"CmdType,attr,omitempty"`
	SNAttr      int     `xml:"SN,attr,omitempty"`
}

func (m *HomePositionQuery) normalize() {
	if m.CmdType == "" {
		m.CmdType = m.CmdTypeAttr
	}
	if m.SN == 0 {
		m.SN = m.SNAttr
	}
}

// HomePositionResponse answers HomePositionQuery; the HomePosition block is
// optional — a device without the capability omits it.
type HomePositionResponse struct {
	XMLName  xml.Name `xml:"Response"`
	CmdType  CmdType  `xml:"CmdType"`
	SN       int      `xml:"SN"`
	DeviceID string   `xml:"DeviceID"`
	// HomePosition: Enabled 0/1; ResetTime in seconds; PresetIndex 0-255.
	HomePosition *HomePositionInfo `xml:"HomePosition,omitempty"`
	CmdTypeAttr  CmdType           `xml:"CmdType,attr,omitempty"`
	SNAttr       int               `xml:"SN,attr,omitempty"`
}

func (m *HomePositionResponse) normalize() {
	if m.CmdType == "" {
		m.CmdType = m.CmdTypeAttr
	}
	if m.SN == 0 {
		m.SN = m.SNAttr
	}
}

// HomePositionInfo is the A.2.6.12 home-position configuration block.
type HomePositionInfo struct {
	Enabled     int `xml:"Enabled"`
	ResetTime   int `xml:"ResetTime,omitempty"`
	PresetIndex int `xml:"PresetIndex,omitempty"`
}

// CruiseTrackListQuery asks for the device's cruise-track list.
type CruiseTrackListQuery struct {
	XMLName     xml.Name `xml:"Query"`
	CmdType     CmdType  `xml:"CmdType"`
	SN          int      `xml:"SN"`
	DeviceID    string   `xml:"DeviceID"`
	CmdTypeAttr CmdType  `xml:"CmdType,attr,omitempty"`
	SNAttr      int      `xml:"SN,attr,omitempty"`
}

func (m *CruiseTrackListQuery) normalize() {
	if m.CmdType == "" {
		m.CmdType = m.CmdTypeAttr
	}
	if m.SN == 0 {
		m.SN = m.SNAttr
	}
}

// CruiseTrackListResponse answers CruiseTrackListQuery. SumNum 0 with the
// optional list omitted means no cruise tracks are configured.
type CruiseTrackListResponse struct {
	XMLName         xml.Name         `xml:"Response"`
	CmdType         CmdType          `xml:"CmdType"`
	SN              int              `xml:"SN"`
	DeviceID        string           `xml:"DeviceID"`
	SumNum          int              `xml:"SumNum"`
	CruiseTrackList *CruiseTrackList `xml:"CruiseTrackList,omitempty"`
	CmdTypeAttr     CmdType          `xml:"CmdType,attr,omitempty"`
	SNAttr          int              `xml:"SN,attr,omitempty"`
}

func (m *CruiseTrackListResponse) normalize() {
	if m.CmdType == "" {
		m.CmdType = m.CmdTypeAttr
	}
	if m.SN == 0 {
		m.SN = m.SNAttr
	}
}

// CruiseTrackList wraps the track entries; Num mirrors len(CruiseTrack).
type CruiseTrackList struct {
	CruiseTrack []CruiseTrackInfo `xml:"CruiseTrack"`
	Num         int               `xml:"Num,attr,omitempty"`
}

// CruiseTrackInfo: Number 0 = first track, 1 = second; Name ≤ 32 bytes.
type CruiseTrackInfo struct {
	Number int    `xml:"Number"`
	Name   string `xml:"Name,omitempty"`
}

// CruiseTrackQuery asks for one cruise track's point list (Number 0/1).
type CruiseTrackQuery struct {
	XMLName     xml.Name `xml:"Query"`
	CmdType     CmdType  `xml:"CmdType"`
	SN          int      `xml:"SN"`
	DeviceID    string   `xml:"DeviceID"`
	Number      int      `xml:"Number"`
	CmdTypeAttr CmdType  `xml:"CmdType,attr,omitempty"`
	SNAttr      int      `xml:"SN,attr,omitempty"`
}

func (m *CruiseTrackQuery) normalize() {
	if m.CmdType == "" {
		m.CmdType = m.CmdTypeAttr
	}
	if m.SN == 0 {
		m.SN = m.SNAttr
	}
}

// CruiseTrackResponse answers CruiseTrackQuery.
type CruiseTrackResponse struct {
	XMLName         xml.Name         `xml:"Response"`
	CmdType         CmdType          `xml:"CmdType"`
	SN              int              `xml:"SN"`
	DeviceID        string           `xml:"DeviceID"`
	Number          int              `xml:"Number"`
	Name            string           `xml:"Name,omitempty"`
	SumNum          int              `xml:"SumNum"`
	CruisePointList *CruisePointList `xml:"CruisePointList,omitempty"`
	CmdTypeAttr     CmdType          `xml:"CmdType,attr,omitempty"`
	SNAttr          int              `xml:"SN,attr,omitempty"`
}

func (m *CruiseTrackResponse) normalize() {
	if m.CmdType == "" {
		m.CmdType = m.CmdTypeAttr
	}
	if m.SN == 0 {
		m.SN = m.SNAttr
	}
}

// CruisePointList wraps the track points; Num mirrors len(CruisePoint).
type CruisePointList struct {
	CruisePoint []CruisePoint `xml:"CruisePoint"`
	Num         int           `xml:"Num,attr,omitempty"`
}

// CruisePoint: a preset stop with dwell seconds and PTZ speed 1-15.
type CruisePoint struct {
	PresetIndex int `xml:"PresetIndex"`
	StayTime    int `xml:"StayTime"`
	Speed       int `xml:"Speed"`
}

// PTZPositionQuery queries or subscribes to a PTZ's precise state.
type PTZPositionQuery struct {
	XMLName     xml.Name `xml:"Query"`
	CmdType     CmdType  `xml:"CmdType"`
	SN          int      `xml:"SN"`
	DeviceID    string   `xml:"DeviceID"`
	CmdTypeAttr CmdType  `xml:"CmdType,attr,omitempty"`
	SNAttr      int      `xml:"SN,attr,omitempty"`
}

func (m *PTZPositionQuery) normalize() {
	if m.CmdType == "" {
		m.CmdType = m.CmdTypeAttr
	}
	if m.SN == 0 {
		m.SN = m.SNAttr
	}
}

// PTZPositionResponse answers PTZPositionQuery; every measurement is
// optional — a fixed camera sends only the identity fields.
type PTZPositionResponse struct {
	XMLName              xml.Name `xml:"Response"`
	CmdType              CmdType  `xml:"CmdType"`
	SN                   int      `xml:"SN"`
	DeviceID             string   `xml:"DeviceID"`
	Pan                  float64  `xml:"Pan,omitempty"`
	Tilt                 float64  `xml:"Tilt,omitempty"`
	Zoom                 float64  `xml:"Zoom,omitempty"`
	HorizontalFieldAngle float64  `xml:"HorizontalFieldAngle,omitempty"`
	VerticalFieldAngle   float64  `xml:"VerticalFieldAngle,omitempty"`
	MaxViewDistance      float64  `xml:"MaxViewDistance,omitempty"`
	CmdTypeAttr          CmdType  `xml:"CmdType,attr,omitempty"`
	SNAttr               int      `xml:"SN,attr,omitempty"`
}

func (m *PTZPositionResponse) normalize() {
	if m.CmdType == "" {
		m.CmdType = m.CmdTypeAttr
	}
	if m.SN == 0 {
		m.SN = m.SNAttr
	}
}

// SDCardStatusQuery asks for the device's storage-card status.
type SDCardStatusQuery struct {
	XMLName     xml.Name `xml:"Query"`
	CmdType     CmdType  `xml:"CmdType"`
	SN          int      `xml:"SN"`
	DeviceID    string   `xml:"DeviceID"`
	CmdTypeAttr CmdType  `xml:"CmdType,attr,omitempty"`
	SNAttr      int      `xml:"SN,attr,omitempty"`
}

func (m *SDCardStatusQuery) normalize() {
	if m.CmdType == "" {
		m.CmdType = m.CmdTypeAttr
	}
	if m.SN == 0 {
		m.SN = m.SNAttr
	}
}

// SDCardStatusResponse answers SDCardStatusQuery. SumNum 0 with the
// optional info block omitted means no card is installed.
type SDCardStatusResponse struct {
	XMLName          xml.Name          `xml:"Response"`
	CmdType          CmdType           `xml:"CmdType"`
	SN               int               `xml:"SN"`
	DeviceID         string            `xml:"DeviceID"`
	SumNum           int               `xml:"SumNum"`
	SDCardStatusInfo *SDCardStatusInfo `xml:"SDCardStatusInfo,omitempty"`
	CmdTypeAttr      CmdType           `xml:"CmdType,attr,omitempty"`
	SNAttr           int               `xml:"SN,attr,omitempty"`
}

func (m *SDCardStatusResponse) normalize() {
	if m.CmdType == "" {
		m.CmdType = m.CmdTypeAttr
	}
	if m.SN == 0 {
		m.SN = m.SNAttr
	}
}

// SDCardStatusInfo holds up to 8 card items; Num mirrors len(Item).
type SDCardStatusInfo struct {
	Item []SDCardItem `xml:"Item"`
	Num  int          `xml:"Num,attr,omitempty"`
}

// SDCardItem: Status ok/formatting/unformatted/idle/error; Capacity and
// FreeSpace in MB; FormatProgress 0-100 while formatting.
type SDCardItem struct {
	ID             int    `xml:"ID"`
	HddName        string `xml:"HddName"`
	Status         string `xml:"Status"`
	FormatProgress int    `xml:"FormatProgress,omitempty"`
	Capacity       int    `xml:"Capacity"`
	FreeSpace      int    `xml:"FreeSpace"`
}
