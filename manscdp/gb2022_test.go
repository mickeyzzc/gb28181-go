package manscdp

import (
	"encoding/xml"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// GB/T 28181-2022 information-query goldens, pinned to the standard text
// (A.2.4.10-14 query commands, A.2.6.12-16 responses): HomePositionQuery,
// CruiseTrackListQuery, CruiseTrackQuery, PTZPosition, SDCardStatus.

func TestGB2022QueryCommandsGolden(t *testing.T) {
	cases := []struct {
		name  string
		v     any
		ctype CmdType
		want  string
	}{
		{
			"HomePositionQuery",
			HomePositionQuery{CmdType: CmdHomePositionQuery, SN: 21, DeviceID: "34020000001320000001"},
			CmdHomePositionQuery,
			"<Query><CmdType>HomePositionQuery</CmdType><SN>21</SN>" +
				"<DeviceID>34020000001320000001</DeviceID></Query>",
		},
		{
			"CruiseTrackListQuery",
			CruiseTrackListQuery{CmdType: CmdCruiseTrackListQuery, SN: 22, DeviceID: "34020000001320000001"},
			CmdCruiseTrackListQuery,
			"<Query><CmdType>CruiseTrackListQuery</CmdType><SN>22</SN>" +
				"<DeviceID>34020000001320000001</DeviceID></Query>",
		},
		{
			"CruiseTrackQuery",
			CruiseTrackQuery{CmdType: CmdCruiseTrackQuery, SN: 23, DeviceID: "34020000001320000001", Number: 1},
			CmdCruiseTrackQuery,
			"<Query><CmdType>CruiseTrackQuery</CmdType><SN>23</SN>" +
				"<DeviceID>34020000001320000001</DeviceID><Number>1</Number></Query>",
		},
		{
			"PTZPosition",
			PTZPositionQuery{CmdType: CmdPTZPosition, SN: 24, DeviceID: "34020000001320000001"},
			CmdPTZPosition,
			"<Query><CmdType>PTZPosition</CmdType><SN>24</SN>" +
				"<DeviceID>34020000001320000001</DeviceID></Query>",
		},
		{
			"SDCardStatus",
			SDCardStatusQuery{CmdType: CmdSDCardStatus, SN: 25, DeviceID: "34020000001320000001"},
			CmdSDCardStatus,
			"<Query><CmdType>SDCardStatus</CmdType><SN>25</SN>" +
				"<DeviceID>34020000001320000001</DeviceID></Query>",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := xml.Marshal(tc.v)
			require.NoError(t, err)
			assert.Equal(t, tc.want, string(out))

			ct, v, err := Decode(append(append([]byte{}, xmlHeader...), out...))
			require.NoError(t, err)
			assert.Equal(t, tc.ctype, ct)
			re, err := xml.Marshal(v)
			require.NoError(t, err)
			assert.Equal(t, string(out), string(re), "decoded value must re-encode byte-identically")
		})
	}
}

func TestGB2022HomePositionResponseRoundTrip(t *testing.T) {
	resp := HomePositionResponse{
		CmdType: CmdHomePositionQuery, SN: 21, DeviceID: "34020000001320000001",
		HomePosition: &HomePositionInfo{Enabled: 1, ResetTime: 10, PresetIndex: 1},
	}
	out, err := xml.Marshal(resp)
	require.NoError(t, err)
	want := "<Response><CmdType>HomePositionQuery</CmdType><SN>21</SN>" +
		"<DeviceID>34020000001320000001</DeviceID>" +
		"<HomePosition><Enabled>1</Enabled><ResetTime>10</ResetTime><PresetIndex>1</PresetIndex></HomePosition></Response>"
	assert.Equal(t, want, string(out))

	ct, v, err := Decode(append(append([]byte{}, xmlHeader...), out...))
	require.NoError(t, err)
	assert.Equal(t, CmdHomePositionQuery, ct)
	re, err := xml.Marshal(v)
	require.NoError(t, err)
	assert.Equal(t, string(out), string(re), "decoded value must re-encode byte-identically")

	// A device without the capability omits the optional block entirely.
	bare := HomePositionResponse{CmdType: CmdHomePositionQuery, SN: 21, DeviceID: "34020000001320000001"}
	out, err = xml.Marshal(bare)
	require.NoError(t, err)
	assert.Contains(t, string(out), "</DeviceID></Response>")
	_, v, err = Decode(append(append([]byte{}, xmlHeader...), out...))
	require.NoError(t, err)
	assert.Nil(t, v.(HomePositionResponse).HomePosition)
}

func TestGB2022CruiseTrackListResponseGolden(t *testing.T) {
	resp := CruiseTrackListResponse{
		CmdType: CmdCruiseTrackListQuery, SN: 22, DeviceID: "34020000001320000001", SumNum: 2,
		CruiseTrackList: &CruiseTrackList{
			Num: 2,
			CruiseTrack: []CruiseTrackInfo{
				{Number: 0, Name: "白天巡航"},
				{Number: 1},
			},
		},
	}
	out, err := xml.Marshal(resp)
	require.NoError(t, err)
	want := "<Response><CmdType>CruiseTrackListQuery</CmdType><SN>22</SN>" +
		"<DeviceID>34020000001320000001</DeviceID><SumNum>2</SumNum>" +
		"<CruiseTrackList Num=\"2\">" +
		"<CruiseTrack><Number>0</Number><Name>白天巡航</Name></CruiseTrack>" +
		"<CruiseTrack><Number>1</Number></CruiseTrack>" +
		"</CruiseTrackList></Response>"
	assert.Equal(t, want, string(out))

	ct, v, err := Decode(append(append([]byte{}, xmlHeader...), out...))
	require.NoError(t, err)
	assert.Equal(t, CmdCruiseTrackListQuery, ct)
	re, err := xml.Marshal(v)
	require.NoError(t, err)
	assert.Equal(t, string(out), string(re), "decoded value must re-encode byte-identically")

	// No tracks configured: optional list omitted, SumNum zero.
	empty := CruiseTrackListResponse{CmdType: CmdCruiseTrackListQuery, SN: 22, DeviceID: "34020000001320000001"}
	_, v, err = Decode(mustEncode(t, empty))
	require.NoError(t, err)
	assert.Zero(t, v.(CruiseTrackListResponse).SumNum)
	assert.Nil(t, v.(CruiseTrackListResponse).CruiseTrackList)
}

func TestGB2022CruiseTrackResponseGolden(t *testing.T) {
	resp := CruiseTrackResponse{
		CmdType: CmdCruiseTrackQuery, SN: 23, DeviceID: "34020000001320000001", Number: 0, Name: "白天巡航", SumNum: 2,
		CruisePointList: &CruisePointList{
			Num: 2,
			CruisePoint: []CruisePoint{
				{PresetIndex: 1, StayTime: 5, Speed: 8},
				{PresetIndex: 2, StayTime: 10, Speed: 4},
			},
		},
	}
	out, err := xml.Marshal(resp)
	require.NoError(t, err)
	want := "<Response><CmdType>CruiseTrackQuery</CmdType><SN>23</SN>" +
		"<DeviceID>34020000001320000001</DeviceID><Number>0</Number><Name>白天巡航</Name>" +
		"<SumNum>2</SumNum>" +
		"<CruisePointList Num=\"2\">" +
		"<CruisePoint><PresetIndex>1</PresetIndex><StayTime>5</StayTime><Speed>8</Speed></CruisePoint>" +
		"<CruisePoint><PresetIndex>2</PresetIndex><StayTime>10</StayTime><Speed>4</Speed></CruisePoint>" +
		"</CruisePointList></Response>"
	assert.Equal(t, want, string(out))

	ct, v, err := Decode(append(append([]byte{}, xmlHeader...), out...))
	require.NoError(t, err)
	assert.Equal(t, CmdCruiseTrackQuery, ct)
	re, err := xml.Marshal(v)
	require.NoError(t, err)
	assert.Equal(t, string(out), string(re), "decoded value must re-encode byte-identically")
}

func TestGB2022PTZPositionResponseGolden(t *testing.T) {
	resp := PTZPositionResponse{
		CmdType: CmdPTZPosition, SN: 24, DeviceID: "34020000001320000001",
		Pan: 123.5, Tilt: -12.25, Zoom: 4.5,
		HorizontalFieldAngle: 52.5, VerticalFieldAngle: 30.0, MaxViewDistance: 200,
	}
	out, err := xml.Marshal(resp)
	require.NoError(t, err)
	want := "<Response><CmdType>PTZPosition</CmdType><SN>24</SN>" +
		"<DeviceID>34020000001320000001</DeviceID>" +
		"<Pan>123.5</Pan><Tilt>-12.25</Tilt><Zoom>4.5</Zoom>" +
		"<HorizontalFieldAngle>52.5</HorizontalFieldAngle>" +
		"<VerticalFieldAngle>30</VerticalFieldAngle>" +
		"<MaxViewDistance>200</MaxViewDistance></Response>"
	assert.Equal(t, want, string(out))

	ct, v, err := Decode(append(append([]byte{}, xmlHeader...), out...))
	require.NoError(t, err)
	assert.Equal(t, CmdPTZPosition, ct)
	re, err := xml.Marshal(v)
	require.NoError(t, err)
	assert.Equal(t, string(out), string(re), "decoded value must re-encode byte-identically")

	// Fixed camera: every measurement is optional and omitted.
	_, v, err = Decode(mustEncode(t, PTZPositionResponse{CmdType: CmdPTZPosition, SN: 24, DeviceID: "34020000001320000001"}))
	require.NoError(t, err)
	assert.Zero(t, v.(PTZPositionResponse).Pan)
}

func TestGB2022SDCardStatusResponseGolden(t *testing.T) {
	resp := SDCardStatusResponse{
		CmdType: CmdSDCardStatus, SN: 25, DeviceID: "34020000001320000001", SumNum: 1,
		SDCardStatusInfo: &SDCardStatusInfo{
			Num: 1,
			Item: []SDCardItem{{
				ID: 0, HddName: "sdcard0", Status: "ok", Capacity: 31914, FreeSpace: 12521,
			}},
		},
	}
	out, err := xml.Marshal(resp)
	require.NoError(t, err)
	want := "<Response><CmdType>SDCardStatus</CmdType><SN>25</SN>" +
		"<DeviceID>34020000001320000001</DeviceID><SumNum>1</SumNum>" +
		"<SDCardStatusInfo Num=\"1\">" +
		"<Item><ID>0</ID><HddName>sdcard0</HddName><Status>ok</Status>" +
		"<Capacity>31914</Capacity><FreeSpace>12521</FreeSpace></Item>" +
		"</SDCardStatusInfo></Response>"
	assert.Equal(t, want, string(out))

	ct, v, err := Decode(append(append([]byte{}, xmlHeader...), out...))
	require.NoError(t, err)
	assert.Equal(t, CmdSDCardStatus, ct)
	re, err := xml.Marshal(v)
	require.NoError(t, err)
	assert.Equal(t, string(out), string(re), "decoded value must re-encode byte-identically")

	// No card installed.
	_, v, err = Decode(mustEncode(t, SDCardStatusResponse{CmdType: CmdSDCardStatus, SN: 25, DeviceID: "34020000001320000001"}))
	require.NoError(t, err)
	assert.Zero(t, v.(SDCardStatusResponse).SumNum)
	assert.Nil(t, v.(SDCardStatusResponse).SDCardStatusInfo)
}

func TestGB2022DecodeAttributeForm(t *testing.T) {
	// CmdType/SN attribute form (the dual-form tolerance every other
	// message carries; some stacks send these queries that way).
	doc := append(append([]byte{}, xmlHeader...),
		[]byte(`<Response CmdType="PTZPosition" SN="7">`+
			`<DeviceID>34020000001320000001</DeviceID><Pan>1.5</Pan></Response>`)...)
	ct, v, err := Decode(doc)
	require.NoError(t, err)
	assert.Equal(t, CmdPTZPosition, ct)
	resp := v.(PTZPositionResponse)
	assert.Equal(t, 7, resp.SN)
	assert.Equal(t, 1.5, resp.Pan)
}

func mustEncode(t *testing.T, v any) []byte {
	t.Helper()
	out, err := xml.Marshal(v)
	require.NoError(t, err)
	return append(append([]byte{}, xmlHeader...), out...)
}
