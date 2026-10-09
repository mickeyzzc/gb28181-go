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

// A.2.3.1.11-13 controls (issue #108).
func TestSend2022ClosureControls(t *testing.T) {
	c, sender, _ := newPTZTestEnv(t, 2)

	require.NoError(t, c.SendDeviceUpgradeCmd("34020000001320000001", manscdp.DeviceUpgradeCmd{
		Firmware:     "v1.0.0",
		FileURL:      "http://192.168.63.30/fw.bin",
		Manufacturer: "MiBee",
		SessionID:    "0123456789abcdef0123456789abcdef",
	}))
	require.Contains(t, sender.body, "<DeviceUpgrade><Firmware>v1.0.0</Firmware>")
	require.Contains(t, sender.body, "<FileURL>http://192.168.63.30/fw.bin</FileURL>")
	require.Contains(t, sender.body, "<SessionID>0123456789abcdef0123456789abcdef</SessionID>")

	require.NoError(t, c.SendFormatSDCardCmd("34020000001320000001", 0))
	require.Contains(t, sender.body, "<FormatSDCard>0</FormatSDCard>")

	require.NoError(t, c.SendPTZPreciseCmd("34020000001320000001", manscdp.PTZPreciseCmd{
		Pan:  float64Ptr(180.5),
		Tilt: float64Ptr(-12.25),
	}))
	require.Contains(t, sender.body, "<PTZPreciseCtrl><Pan>180.5</Pan><Tilt>-12.25</Tilt></PTZPreciseCtrl>")

	require.ErrorIs(t, c.SendFormatSDCardCmd("34020000001329999999", 0), ErrChannelNotFound)
}

func float64Ptr(v float64) *float64 { return &v }
