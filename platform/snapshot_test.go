package platform

import (
	"testing"

	"github.com/mickeyzzc/gb28181-go/manscdp"
	"github.com/stretchr/testify/require"
)

// GB/T 28181-2022 snapshot control + manual recording convenience (#49).
func TestSendSnapShotCmd(t *testing.T) {
	c, sender, _ := newPTZTestEnv(t, 2)

	err := c.SendSnapShotCmd("34020000001320000001", manscdp.SnapShotCmd{
		SnapNum:   3,
		Interval:  2,
		UploadURL: "http://192.168.63.30:9090/api/gb28181/snapshot/upload",
		SessionID: "0123456789abcdef0123456789abcdef",
	})
	require.NoError(t, err)
	// The snapshot rides the device-config channel (A.2.1.24): Control
	// root, CmdType=DeviceConfig, <SnapShotConfig> payload (issue #107).
	require.Contains(t, sender.body, "<CmdType>DeviceConfig</CmdType>")
	require.Contains(t, sender.body, "<SnapShotConfig><SnapNum>3</SnapNum><Interval>2</Interval>")
	require.Contains(t, sender.body, "<UploadURL>http://192.168.63.30:9090/api/gb28181/snapshot/upload</UploadURL>")

	// Offline and missing channels behave like every other control.
	require.ErrorIs(t, c.SendSnapShotCmd("34020000001329999999", manscdp.SnapShotCmd{SnapNum: 1}), ErrChannelNotFound)
}

func TestManualRecordConvenience(t *testing.T) {
	c, sender, _ := newPTZTestEnv(t, 2)

	require.NoError(t, c.StartManualRecord("34020000001320000001"))
	require.Contains(t, sender.body, "<RecordCmd>Record</RecordCmd>")

	require.NoError(t, c.StopManualRecord("34020000001320000001"))
	require.Contains(t, sender.body, "<RecordCmd>StopRecord</RecordCmd>")
}
