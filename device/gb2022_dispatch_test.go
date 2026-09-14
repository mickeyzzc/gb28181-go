package device

import (
	"strings"
	"testing"
)

// GB/T 28181-2022 information queries (A.2.4.10-14) must be answered with
// the minimal valid Response forms (A.2.6.12-16) instead of falling through
// the unknown-CmdType warn + silence: this device has no PTZ hardware,
// cruise tracks, or storage card, so every optional block is omitted and
// the required SumNum fields are zero.
func TestDispatch_GB2022InformationQueries(t *testing.T) {
	cases := []struct {
		name string
		body string
		want []string
	}{
		{
			"HomePositionQuery",
			`<Query><CmdType>HomePositionQuery</CmdType><SN>61</SN><DeviceID>dev</DeviceID></Query>`,
			[]string{"HomePositionQuery", `SN="61"`, "<DeviceID>dev</DeviceID>"},
		},
		{
			"CruiseTrackListQuery",
			`<Query><CmdType>CruiseTrackListQuery</CmdType><SN>62</SN><DeviceID>dev</DeviceID></Query>`,
			[]string{"CruiseTrackListQuery", "<SumNum>0</SumNum>"},
		},
		{
			"CruiseTrackQuery",
			`<Query><CmdType>CruiseTrackQuery</CmdType><SN>63</SN><DeviceID>dev</DeviceID><Number>1</Number></Query>`,
			[]string{"CruiseTrackQuery", "<Number>1</Number>", "<SumNum>0</SumNum>"},
		},
		{
			"PTZPosition",
			`<Query><CmdType>PTZPosition</CmdType><SN>64</SN><DeviceID>dev</DeviceID></Query>`,
			[]string{"PTZPosition", "<DeviceID>dev</DeviceID>"},
		},
		{
			"SDCardStatus",
			`<Query><CmdType>SDCardStatus</CmdType><SN>65</SN><DeviceID>dev</DeviceID></Query>`,
			[]string{"SDCardStatus", "<SumNum>0</SumNum>"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg := buildInboundMessage(tc.body)
			ok200, queued, err := DispatchInboundMessage(msg, testDeviceContext(), nil)
			if err != nil {
				t.Fatalf("DispatchInboundMessage failed: %v", err)
			}
			if ok200.StatusCode != 200 {
				t.Errorf("Expected 200 OK, got status %d", ok200.StatusCode)
			}
			if queued == nil {
				t.Fatal("Expected a queued response, got nil")
			}
			for _, w := range tc.want {
				if !strings.Contains(queued.Body, w) {
					t.Errorf("queued body missing %q:\n%s", w, queued.Body)
				}
			}
			// None of the minimal answers may carry a payload block
			// (element form, not the CmdType attribute value).
			for _, absent := range []string{"<HomePosition", "<CruiseTrackList", "<CruisePointList", "<SDCardStatusInfo", "<Pan>"} {
				if strings.Contains(queued.Body, absent) {
					t.Errorf("minimal answer must not carry %q:\n%s", absent, queued.Body)
				}
			}
		})
	}
}
