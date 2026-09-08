package hygiene_test

import (
	"testing"

	gbdev "github.com/mickeyzzc/gb28181-go/device"
	"github.com/mickeyzzc/gb28181-go/manscdp"
	"github.com/mickeyzzc/gb28181-go/nalutil"
	"github.com/mickeyzzc/gb28181-go/platform"
)

// Fuzz targets pin the "hostile input never panics or hangs" invariant for
// every parser facing an untrusted network (issue #39). The seed corpus
// runs on every normal `go test`; extended fuzzing is `go test -fuzz`.

func FuzzDeviceParseSIP(f *testing.F) {
	f.Add([]byte("REGISTER sip:3402000000@3402000000 SIP/2.0\r\nVia: SIP/2.0/UDP 1.2.3.4:5060\r\nFrom: <sip:34020000001320000001@3402000000>;tag=1\r\nTo: <sip:3402000000@3402000000>\r\nCall-ID: 1@x\r\nCSeq: 1 REGISTER\r\nContent-Length: 0\r\n\r\n"))
	f.Add([]byte("SIP/2.0 401 Unauthorized\r\nWWW-Authenticate: Digest realm=\"3402000000\", nonce=\"abc\"\r\nContent-Length: 0\r\n\r\n"))
	f.Add([]byte("MESSAGE sip:x SIP/2.0\r\nContent-Type: Application/MANSCDP+xml\r\nContent-Length: 5\r\n\r\nhello"))
	f.Add([]byte("\r\n\r\n"))
	f.Add([]byte{0x00, 0xff, 0xfe})
	f.Fuzz(func(t *testing.T, data []byte) {
		if _, err := gbdev.Parse(data); err == nil && len(data) > 1<<20 {
			t.Fatalf("accepted oversized message")
		}
	})
}

func FuzzManscdpDecode(f *testing.F) {
	f.Add([]byte(`<?xml version="1.0"?><Notify><CmdType>Keepalive</CmdType><SN>1</SN><DeviceID>34020000001320000001</DeviceID><Status>OK</Status></Notify>`))
	f.Add([]byte(`<?xml version="1.0"?><Response><CmdType>DeviceInfo</CmdType><SN>2</SN><DeviceID>34020000001320000001</DeviceID></Response>`))
	f.Add([]byte("<Query>"))
	f.Add([]byte{0x81, 0x40, 0x30, 0x00}) // GBK-ish bytes
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _, _ = manscdp.Decode(data)
	})
}

func FuzzNalutil(f *testing.F) {
	f.Add([]byte{0x67, 0x64, 0x00, 0x1f})
	f.Add([]byte{0x65, 0x01})
	f.Add([]byte{0x00, 0x00, 0x00, 0x01, 0x67, 0x64})
	f.Add([]byte{0x7c, 0x01})
	f.Fuzz(func(t *testing.T, nalu []byte) {
		_ = nalutil.IsKeyframeNALU(nalu, false)
		_ = nalutil.IsKeyframeNALU(nalu, true)
		au := [][]byte{nalu, {0x65, 0x01}}
		_ = nalutil.IsIDR(au, false)
		nalutil.ExtractParamSetsH264(au)
		nalutil.ExtractParamSetsH265(au)
		_ = nalutil.HasCompleteParamSets(au, false)
	})
}

func FuzzPSDemuxerFeed(f *testing.F) {
	f.Add([]byte{0x00, 0x00, 0x01, 0xba}, int64(90000), true)
	f.Add([]byte{0x00, 0x00, 0x01, 0xe0, 0x00, 0x10}, int64(0), false)
	f.Add([]byte{0x67, 0x64, 0x00, 0x1f, 0x65, 0x01}, int64(1), true)
	f.Add([]byte{}, int64(0), true)
	f.Fuzz(func(t *testing.T, payload []byte, pts int64, complete bool) {
		d := platform.NewPSDemuxer()
		// Repeated feeds exercise stateful demux paths (partial AU
		// accumulation, PES overflow guards).
		_, _ = d.FeedAU(payload, pts, complete)
		_, _ = d.FeedAU(payload, pts+1, !complete)
		d.DropPartialVideo()
	})
}
