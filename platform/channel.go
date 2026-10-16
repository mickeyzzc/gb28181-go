package platform

import (
	"strconv"
	"sync/atomic"
)

// Channel state values (Channel.Status).
const (
	// ChannelIdle means no media session is active for the channel.
	ChannelIdle int32 = 0
	// ChannelInviting means an INVITE was sent but the device has not
	// answered with a successful 200 OK / media start yet.
	ChannelInviting int32 = 1
	// ChannelPlaying means an INVITE succeeded and RTP is being received.
	ChannelPlaying int32 = 2
)

// GB/T 28181-2022 Table E.1 type codes (digits 11-13 of the 20-digit
// device/channel ID). The list keeps the codes hosts actually branch on;
// the full table is 111-215 with gaps.
const (
	ChannelTypeCamera      = 131 // 摄像机编码
	ChannelTypeIPC         = 132 // 网络摄像机（IPC）/在线视频图像信息采集设备
	ChannelTypeAudioInput  = 136 // 语音输入设备编码 — network pickup, audio-only
	ChannelTypeAudioOutput = 137 // 语音输出设备编码
)

// ChannelTypeCode extracts the type code (digits 11-13) from a 20-digit
// GB/T 28181 device/channel ID (Table E.1). 0 when the ID is too short or
// the slice is not numeric.
func ChannelTypeCode(id string) int {
	if len(id) < 13 {
		return 0
	}
	n, err := strconv.Atoi(id[10:13])
	if err != nil {
		return 0
	}
	return n
}

// IsAudioInputChannel reports whether the ID marks a voice-input device
// (type 136, 网络拾音器): an audio-only channel with no video track. The
// catalog needs no extra capability bit — the standard's own type code is
// the marker (issue #135).
func IsAudioInputChannel(id string) bool {
	return ChannelTypeCode(id) == ChannelTypeAudioInput
}

// Channel represents a video channel within a GB28181 device. Channels are
// created from the device's Catalog response (see RegisterChannel) and their
// Status transitions are driven by the session manager (idle → inviting →
// playing → idle).
type Channel struct {
	DeviceID string
	ID       string
	Name     string
	Parental int // 0=parental, 1=subordinate
	Status   atomic.Int32
	Device   *Device // back-reference, set by DeviceManager.RegisterChannel
	// PTZType is the GB/T 28181-2016 § 5.4.12 PTZ capability code from the
	// Catalog item: 0 = no PTZ, 1 = pan/tilt, 2 = pan/tilt + zoom.
	PTZType int
	// SubProbe marks a channel that was NOT in the device catalog but was
	// registered by the sub-channel prober (#560) from the vendor-convention
	// code offset. Catalog refreshes never list it, so the flag distinguishes
	// "real channel the device advertises" from "synthetic pull target".
	SubProbe bool
	// TypeCode is the Table E.1 type code derived from the channel ID
	// (ChannelTypeCode); 0 for non-standard IDs.
	TypeCode int
	// AudioOnly marks a voice-input channel (TypeCode 136, 网络拾音器):
	// the live INVITE negotiates m=audio only (G.711 A-law, PT 8) and the
	// receiver never expects a video track (issue #135).
	AudioOnly bool
}
