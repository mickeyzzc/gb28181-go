package device

import (
	"encoding/xml"
	"log/slog"
)

// Host data for the 2022 five information queries (A.2.4.10-14 →
// A.2.6.12-16). Install via SetInfoQueryProvider; without one the
// answers stay the minimal valid (empty) form.

// HomePositionInfo fills the A.2.6.12 HomePositionQuery answer.
type HomePositionInfo struct {
	Enabled      int // 0-off, 1-on
	ResetTimeSec int // auto-return interval, seconds (0 = omit)
	PresetIndex  int // preset to recall, 0..=255 (negative = omit)
}

// CruiseTrackInfo is one A.2.6.13 track-list entry.
type CruiseTrackInfo struct {
	Number int    // 0 = first track
	Name   string // ≤ 32 bytes
}

// CruisePointInfo is one A.2.6.14 track point.
type CruisePointInfo struct {
	PresetIndex  int
	StayTimeSecs int
	Speed        int // 1..=15
}

// CruiseTrackDetail fills the A.2.6.14 CruiseTrackQuery answer.
type CruiseTrackDetail struct {
	Number int
	Name   string
	Points []CruisePointInfo
}

// PtzPositionInfo fills the A.2.6.15 PTZPosition answer; nil fields
// are omitted per the schema.
type PtzPositionInfo struct {
	Pan                  *float64
	Tilt                 *float64
	Zoom                 *float64
	HorizontalFieldAngle *float64
	VerticalFieldAngle   *float64
	MaxViewDistance      *float64
}

// SdCardInfo is one A.2.6.16 storage-card entry.
type SdCardInfo struct {
	ID             int
	HddName        string
	Status         string // ok | formatting | unformatted | idle | error
	FormatProgress int    // 0-100 (%), negative = omit
	CapacityMB     int64
	FreeSpaceMB    int64
}

// InfoQueryProvider supplies the 2022 five information-query answers.
// A nil return keeps the minimal valid answer.
type InfoQueryProvider interface {
	HomePosition() *HomePositionInfo
	CruiseTrackList() []CruiseTrackInfo
	CruiseTrack(number int) *CruiseTrackDetail
	PtzPosition() *PtzPositionInfo
	SdCardStatus() []SdCardInfo
}

// SetInfoQueryProvider installs the host data provider for the 2022
// five information queries (A.2.6.12-16); nil (default) keeps the
// minimal answers.
func (s *Server) SetInfoQueryProvider(p InfoQueryProvider) {
	s.mu.Lock()
	s.infoQueries = p
	s.mu.Unlock()
	if p != nil {
		s.devCtx.InfoQueries = p
	}
}

// ---------------------------------------------------------------------------
// A.2.6.12-16 answer builders (provider data present). The minimal
// forms live in BuildGB2022QueryResponseMessage.
// ---------------------------------------------------------------------------

type homePositionResponse struct {
	XMLName      xml.Name `xml:"Response"`
	CmdType      string   `xml:"CmdType,attr"`
	SN           string   `xml:"SN,attr"`
	DeviceID     string   `xml:"DeviceID"`
	HomePosition *struct {
		Enabled     int `xml:"Enabled"`
		ResetTime   int `xml:"ResetTime,omitempty"`
		PresetIndex int `xml:"PresetIndex,omitempty"`
	} `xml:"HomePosition"`
}

type cruiseTrackListResponse struct {
	XMLName  xml.Name `xml:"Response"`
	CmdType  string   `xml:"CmdType,attr"`
	SN       string   `xml:"SN,attr"`
	DeviceID string   `xml:"DeviceID"`
	SumNum   int      `xml:"SumNum"`
	Tracks   *struct {
		Track []struct {
			Number int    `xml:"Number"`
			Name   string `xml:"Name,omitempty"`
		} `xml:"CruiseTrack"`
	} `xml:"CruiseTrackList"`
}

type cruiseTrackResponse struct {
	XMLName  xml.Name `xml:"Response"`
	CmdType  string   `xml:"CmdType,attr"`
	SN       string   `xml:"SN,attr"`
	DeviceID string   `xml:"DeviceID"`
	Number   int      `xml:"Number"`
	Name     string   `xml:"Name,omitempty"`
	SumNum   int      `xml:"SumNum"`
	Points   *struct {
		Point []struct {
			PresetIndex  int `xml:"PresetIndex"`
			StayTimeSecs int `xml:"StayTime"`
			Speed        int `xml:"Speed"`
		} `xml:"CruisePoint"`
	} `xml:"CruisePointList"`
}

type ptzPositionResponse struct {
	XMLName              xml.Name `xml:"Response"`
	CmdType              string   `xml:"CmdType,attr"`
	SN                   string   `xml:"SN,attr"`
	DeviceID             string   `xml:"DeviceID"`
	Pan                  *float64 `xml:"Pan,omitempty"`
	Tilt                 *float64 `xml:"Tilt,omitempty"`
	Zoom                 *float64 `xml:"Zoom,omitempty"`
	HorizontalFieldAngle *float64 `xml:"HorizontalFieldAngle,omitempty"`
	VerticalFieldAngle   *float64 `xml:"VerticalFieldAngle,omitempty"`
	MaxViewDistance      *float64 `xml:"MaxViewDistance,omitempty"`
}

type sdCardStatusResponse struct {
	XMLName  xml.Name `xml:"Response"`
	CmdType  string   `xml:"CmdType,attr"`
	SN       string   `xml:"SN,attr"`
	DeviceID string   `xml:"DeviceID"`
	SumNum   int      `xml:"SumNum"`
	Info     *struct {
		Item []struct {
			ID             int    `xml:"ID"`
			HddName        string `xml:"HddName"`
			Status         string `xml:"Status"`
			FormatProgress int    `xml:"FormatProgress,omitempty"`
			CapacityMB     int64  `xml:"Capacity"`
			FreeSpaceMB    int64  `xml:"FreeSpace"`
		} `xml:"Item"`
	} `xml:"SDCardStatusInfo"`
}

// BuildInfoQueryResponseMessage answers one of the 2022 five
// information queries with provider data (wire shapes per A.2.6.12-16).
// number only applies to CruiseTrackQuery.
func BuildInfoQueryResponseMessage(cmdType, sn, deviceID string, number int, p InfoQueryProvider) SipMessage {
	switch cmdType {
	case "HomePositionQuery":
		body := homePositionResponse{CmdType: cmdType, SN: sn, DeviceID: deviceID}
		if info := p.HomePosition(); info != nil {
			hp := &struct {
				Enabled     int `xml:"Enabled"`
				ResetTime   int `xml:"ResetTime,omitempty"`
				PresetIndex int `xml:"PresetIndex,omitempty"`
			}{Enabled: info.Enabled}
			if info.ResetTimeSec > 0 {
				hp.ResetTime = info.ResetTimeSec
			}
			if info.PresetIndex >= 0 {
				hp.PresetIndex = info.PresetIndex
			}
			body.HomePosition = hp
		}
		return marshalInfo(body)
	case "CruiseTrackListQuery":
		body := cruiseTrackListResponse{CmdType: cmdType, SN: sn, DeviceID: deviceID}
		if tracks := p.CruiseTrackList(); tracks != nil {
			list := &struct {
				Track []struct {
					Number int    `xml:"Number"`
					Name   string `xml:"Name,omitempty"`
				} `xml:"CruiseTrack"`
			}{}
			for _, t := range tracks {
				list.Track = append(list.Track, struct {
					Number int    `xml:"Number"`
					Name   string `xml:"Name,omitempty"`
				}{t.Number, t.Name})
			}
			body.SumNum = len(tracks)
			body.Tracks = list
		}
		return marshalInfo(body)
	case "CruiseTrackQuery":
		body := cruiseTrackResponse{CmdType: cmdType, SN: sn, DeviceID: deviceID, Number: number}
		if d := p.CruiseTrack(number); d != nil {
			body.Name = d.Name
			list := &struct {
				Point []struct {
					PresetIndex  int `xml:"PresetIndex"`
					StayTimeSecs int `xml:"StayTime"`
					Speed        int `xml:"Speed"`
				} `xml:"CruisePoint"`
			}{}
			for _, pt := range d.Points {
				list.Point = append(list.Point, struct {
					PresetIndex  int `xml:"PresetIndex"`
					StayTimeSecs int `xml:"StayTime"`
					Speed        int `xml:"Speed"`
				}{pt.PresetIndex, pt.StayTimeSecs, pt.Speed})
			}
			body.SumNum = len(d.Points)
			body.Points = list
		}
		return marshalInfo(body)
	case "PTZPosition":
		body := ptzPositionResponse{CmdType: cmdType, SN: sn, DeviceID: deviceID}
		if pos := p.PtzPosition(); pos != nil {
			body.Pan, body.Tilt, body.Zoom = pos.Pan, pos.Tilt, pos.Zoom
			body.HorizontalFieldAngle = pos.HorizontalFieldAngle
			body.VerticalFieldAngle = pos.VerticalFieldAngle
			body.MaxViewDistance = pos.MaxViewDistance
		}
		return marshalInfo(body)
	case "SDCardStatus":
		body := sdCardStatusResponse{CmdType: cmdType, SN: sn, DeviceID: deviceID}
		if cards := p.SdCardStatus(); cards != nil {
			if len(cards) > 8 {
				cards = cards[:8]
			}
			info := &struct {
				Item []struct {
					ID             int    `xml:"ID"`
					HddName        string `xml:"HddName"`
					Status         string `xml:"Status"`
					FormatProgress int    `xml:"FormatProgress,omitempty"`
					CapacityMB     int64  `xml:"Capacity"`
					FreeSpaceMB    int64  `xml:"FreeSpace"`
				} `xml:"Item"`
			}{}
			for _, c := range cards {
				item := struct {
					ID             int    `xml:"ID"`
					HddName        string `xml:"HddName"`
					Status         string `xml:"Status"`
					FormatProgress int    `xml:"FormatProgress,omitempty"`
					CapacityMB     int64  `xml:"Capacity"`
					FreeSpaceMB    int64  `xml:"FreeSpace"`
				}{c.ID, c.HddName, c.Status, c.FormatProgress, c.CapacityMB, c.FreeSpaceMB}
				info.Item = append(info.Item, item)
			}
			body.SumNum = len(cards)
			body.Info = info
		}
		return marshalInfo(body)
	}
	return SipMessage{}
}

func marshalInfo(body any) SipMessage {
	xmlData, err := xml.Marshal(body)
	if err != nil {
		slog.Error("Failed to marshal info-query response", "error", err)
		return SipMessage{}
	}
	return SipMessage{
		Method:      "MESSAGE",
		ContentType: "Application/MANSCDP+xml",
		Body:        string(xmlData),
		UserAgent:   UserAgent,
		Headers:     make(map[string]string),
	}
}
