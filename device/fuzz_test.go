package device

import (
	"encoding/hex"
	"strings"
	"testing"
)

// Fuzz targets pin the "hostile datagram / payload never panics"
// invariant for the untrusted-input parse surface: raw SIP datagrams,
// PTZCmd hex payloads, WWW-Authenticate challenges, SDP bodies, and
// SIP Date headers. The seed corpus runs on every normal `go test`;
// extended fuzzing is `go test -fuzz`.

func FuzzSipParse(f *testing.F) {
	f.Add([]byte("REGISTER sip:3402000000@3402000000 SIP/2.0\r\nVia: SIP/2.0/UDP 192.168.1.100:5060;branch=z9hG4bK12345\r\nFrom: <sip:34020000012000000001@3402000000>;tag=12345\r\nTo: <sip:3402000000@3402000000>\r\nCall-ID: 1234567890@192.168.1.100\r\nCSeq: 1 REGISTER\r\nContact: <sip:34020000012000000001@192.168.1.100:5060>\r\nExpires: 3600\r\nContent-Length: 0\r\n\r\n"))
	f.Add([]byte("SIP/2.0 200 OK\r\nVia: SIP/2.0/UDP 192.168.1.100:5060\r\nFrom: <sip:a@b>\r\nTo: <sip:a@b>;tag=t\r\nCall-ID: 1\r\nCSeq: 1 REGISTER\r\nDate: Sun, 20 Sep 2026 10:00:00 +0800\r\nContent-Length: 0\r\n\r\n"))
	f.Add([]byte("MESSAGE sip:34020000001320000002@3402000000 SIP/2.0\r\nContent-Type: Application/MANSRTSP\r\nContent-Length: 4\r\n\r\nabcd"))
	f.Add([]byte("garbage"))
	f.Add([]byte{0x00, 0xff, 0xfe})
	f.Fuzz(func(t *testing.T, data []byte) {
		// No-panic only: field-level invariants on a parsed hostile
		// message would false-positive — half-formed messages are the
		// parser's legitimate output on truncated datagrams.
		_, _ = Parse(data)
	})
}

func FuzzDecodePTZCommand(f *testing.F) {
	f.Add("A50F0100000000B5") // stop
	f.Add("A50F0108002000DD") // up
	f.Add("A50F0181000006D3") // preset set + Goto
	f.Add("A50F01B0000002" + "3E")
	f.Add("a5 0f 01 01 00 00 00") // short
	f.Add("ZZZZ")
	f.Add("")
	f.Fuzz(func(t *testing.T, a505Hex string) {
		cmd := DecodePTZCommand(a505Hex)
		// The decoder is total and byte-faithful: whatever came in, the
		// result is either PtzInvalid with the trimmed input preserved,
		// or a structurally valid command (8 bytes, A5 start, checksum).
		trimmed := strings.TrimSpace(a505Hex)
		if cmd.Kind == PtzInvalid {
			if cmd.RawHex != trimmed {
				t.Errorf("DecodePTZCommand: PtzInvalid must preserve RawHex, got %q want %q", cmd.RawHex, trimmed)
			}
			return
		}
		raw, err := hex.DecodeString(trimmed)
		if err != nil || len(raw) != 8 {
			t.Fatalf("DecodePTZCommand: non-invalid kind but input is not 8 hex bytes: %q", trimmed)
		}
		if raw[0] != 0xA5 {
			t.Fatalf("DecodePTZCommand: non-invalid kind but start byte is %#x", raw[0])
		}
		var sum byte
		for _, b := range raw[:7] {
			sum += b
		}
		if sum != raw[7] {
			t.Fatalf("DecodePTZCommand: non-invalid kind but checksum mismatch: %q", trimmed)
		}
	})
}

func FuzzParseChallenge(f *testing.F) {
	f.Add(`Digest realm="3402000000", nonce="abc123", algorithm=MD5`)
	f.Add(`Digest nonce="n==",algorithm=SM3`)
	f.Add(`Basic realm="x"`)
	f.Add("")
	f.Add(`Digest realm="unterminated`)
	f.Fuzz(func(t *testing.T, header string) {
		// No-panic only: a syntactically odd challenge may parse to a
		// partial DigestAuth with an error — call sites act on the error.
		_, _ = ParseChallenge(header)
	})
}

func FuzzParseSDP(f *testing.F) {
	f.Add("v=0\r\no=34020000001320000002 0 0 IN IP4 192.168.1.100\r\ns=Play\r\nc=IN IP4 192.168.63.30\r\nt=0 0\r\nm=audio 30000 RTP/AVP 8\r\ny=200006001\r\n")
	f.Add("v=0\r\no=1 0 0 IN IP4 10.0.0.1\r\ns=Playback\r\nt=1695000000 1695003600\r\nm=video 0 RTP/AVP 96\r\na=recvonly\r\na=rtpmap:96 H264/90000\r\n")
	f.Add("not sdp at all")
	f.Add("")
	f.Fuzz(func(t *testing.T, body string) {
		// No-panic only: partial SDP legitimately yields some fields and
		// an error.
		_, _, _, _, _ = parseSDP(body)
		_, _ = parseSDPTimeRange(body)
	})
}

func FuzzParseSIPDate(f *testing.F) {
	f.Add("Sun, 20 Sep 2026 10:00:00 +0800")
	f.Add("Sun, 20 Sep 2026 02:00:00 GMT")
	f.Add("Sunday, 20-Sep-26 10:00:00 +0800")
	f.Add("bogus")
	f.Add("")
	f.Fuzz(func(t *testing.T, value string) {
		// No-panic only; ok=false is the contract for unparsable dates.
		_, _ = ParseSIPDate(value)
	})
}
