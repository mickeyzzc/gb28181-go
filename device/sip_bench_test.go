package device

// SIP wire parsing benchmarks (issue #45): the parse cost of the two
// messages that dominate real traffic — the REGISTER refresh and the
// MANSCDP MESSAGE.

import "testing"

const benchRegisterWire = "REGISTER sip:3402000000@3402000000 SIP/2.0\r\n" +
	"Via: SIP/2.0/UDP 192.168.62.10:5060;rport;branch=z9hG4bK1234567890abcdef\r\n" +
	"From: <sip:34020000001320000001@3402000000>;tag=abc123\r\n" +
	"To: <sip:34020000001320000001@3402000000>\r\n" +
	"Call-ID: 1757000000@192.168.62.10\r\n" +
	"CSeq: 1 REGISTER\r\n" +
	"Contact: <sip:34020000001320000001@192.168.62.10:5060>\r\n" +
	"Max-Forwards: 70\r\n" +
	"Expires: 3600\r\n" +
	"Content-Length: 0\r\n\r\n"

const benchCatalogWire = "MESSAGE sip:34020000002000000001@3402000000 SIP/2.0\r\n" +
	"Via: SIP/2.0/UDP 192.168.62.10:5060;rport;branch=z9hG4bKfedcba0987654321\r\n" +
	"From: <sip:34020000001320000001@3402000000>;tag=def456\r\n" +
	"To: <sip:34020000002000000001@3402000000>\r\n" +
	"Call-ID: 1757000001@192.168.62.10\r\n" +
	"CSeq: 2 MESSAGE\r\n" +
	"Max-Forwards: 70\r\n" +
	"Content-Type: Application/MANSCDP+xml\r\n" +
	"Content-Length: 190\r\n\r\n" +
	"<?xml version=\"1.0\"?><Response><CmdType>Catalog</CmdType><SN>1</SN><DeviceID>34020000001320000001</DeviceID><SumNum>1</SumNum><DeviceList Num=\"1\"><Item><DeviceID>34020000001320000001</DeviceID><Name>Camera</Name><Manufacturer>Unknown</Manufacturer><Model>Unknown</Model><Status>ON</Status></Item></DeviceList></Response>"

func BenchmarkSIPParseRegister(b *testing.B) {
	for range b.N {
		if _, err := Parse([]byte(benchRegisterWire)); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkSIPParseCatalogMessage(b *testing.B) {
	for range b.N {
		if _, err := Parse([]byte(benchCatalogWire)); err != nil {
			b.Fatal(err)
		}
	}
}
