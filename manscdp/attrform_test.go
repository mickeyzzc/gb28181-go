package manscdp

// Attribute-form fallback coverage: real devices emit MANSCDP with
// CmdType/SN as ROOT ATTRIBUTES instead of child elements; every
// normalize() must pick them up. Also the short-server-ID branch of
// buildSSRC.

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDecodeAttributeFormFallbacks(t *testing.T) {
	cases := []struct {
		name string
		body string
		want CmdType
		sn   int
	}{
		{"keepalive", `<Notify CmdType="Keepalive" SN="3"><Status>OK</Status></Notify>`, CmdKeepalive, 3},
		{"deviceStatus", `<Response CmdType="DeviceStatus" SN="4"><DeviceID>34020000001320000001</DeviceID></Response>`, CmdDeviceStatus, 4},
		{"recordInfo", `<Response CmdType="RecordInfo" SN="5"><DeviceID>34020000001320000001</DeviceID></Response>`, CmdRecordInfo, 5},
		{"deviceControl", `<Control CmdType="DeviceControl" SN="6"><DeviceID>34020000001320000001</DeviceID></Control>`, CmdDeviceControl, 6},
		{"broadcast", `<Notify CmdType="Broadcast" SN="7"><SourceID>34020000002000000001</SourceID><TargetID>34020000001320000001</TargetID></Notify>`, CmdBroadcast, 7},
		{"alarm", `<Notify CmdType="Alarm" SN="8"><DeviceID>34020000001320000001</DeviceID></Notify>`, CmdAlarm, 8},
		{"mobilePosition", `<Notify CmdType="MobilePosition" SN="9"><DeviceID>34020000001320000001</DeviceID></Notify>`, CmdMobilePosition, 9},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd, _, err := Decode([]byte(tc.body))
			require.NoError(t, err)
			require.Equal(t, tc.want, cmd, "CmdType must fall back to the attribute form")
		})
	}
}

func TestBuildSSRCServerIDLengths(t *testing.T) {
	// Canonical 10-digit layout: prefix + 5-digit domain + 4-digit seq.
	require.Len(t, SSRC(false, "34020000002000000001", 1), 10)

	// Short-but-usable IDs pad the domain with leading zeros.
	ssrc := SSRC(false, "34021234", 5)
	require.Equal(t, "212340005", ssrc[1:], "domain 21234 from ID[3:8]")

	// Tiny IDs fall back to the all-zero domain.
	ssrc = SSRC(true, "340", 7)
	require.Equal(t, "1000000007", ssrc, "prefix=1 playback, zero-domain fallback, seq 7")
}
