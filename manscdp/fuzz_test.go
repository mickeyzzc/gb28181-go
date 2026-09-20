package manscdp

import (
	"testing"
)

// Fuzz targets pin the "hostile SIP MESSAGE body never panics" invariant
// for the untrusted MANSCDP XML surface. The seed corpus runs on every
// normal `go test`; extended fuzzing is `go test -fuzz`.

func FuzzDecode(f *testing.F) {
	f.Add([]byte(`<?xml version="1.0"?><Notify><CmdType>Catalog</CmdType><SN>1</SN><DeviceList Num="0"/></Notify>`))
	f.Add([]byte(`<Response><CmdType>DeviceInfo</CmdType><SN>2</SN></Response>`))
	f.Add([]byte(`<Query><CmdType>RecordInfo</CmdType><SN>3</SN><StreamProfile>1</StreamProfile></Query>`))
	f.Add([]byte(`<Control><CmdType>PTZCmd</CmdType><SN>4</SN><PTZCmd>A50F0100000000B5</PTZCmd></Control>`))
	// GBK bytes: "监控" in GB18030 — exercises the charset-conversion path.
	f.Add([]byte{0xd6, 0xd0, 0xb9, 0xfa, '<', 'N', 'o', 't', 'i', 'f', 'y', '/', '>'})
	f.Add([]byte("<Unclosed"))
	f.Add([]byte{0x00, 0xff, 0xfe, 0xfd})
	f.Fuzz(func(t *testing.T, data []byte) {
		ct, _, err := Decode(data)
		// A nil error always identifies a CmdType (decodeOnce rejects a
		// missing one); an empty CmdType with nil error would leave every
		// caller's type switch dangling.
		if err == nil && ct == "" {
			t.Error("Decode: nil error but empty CmdType")
		}
	})
}
