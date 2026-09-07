//go:build gb35114

package security35114

import (
	"encoding/base64"
	"encoding/hex"
	"testing"
	"time"
)

// Keepalive-shaped inputs mirroring a real MESSAGE with the Note header.
const (
	noteFrom   = "<sip:34020000001320000001@3402000000>;tag=1465468922"
	noteTo     = "<sip:34020000002000000001@3402000000>"
	noteCallID = "1465470512602@192.168.1.100"
	noteBody   = "<?xml version=\"1.0\"?>\r\n<Notify>\r\n<CmdType>Keepalive</CmdType>\r\n<SN>1</SN>\r\n<DeviceID>34020000001320000001</DeviceID>\r\n<Status>OK</Status>\r\n</Notify>\r\n"
)

func TestDigestPayloadVKEKRawGolden(t *testing.T) {
	got := DigestPayload("MESSAGE", noteFrom, noteTo, noteCallID, goldenDate, goldenVKEK[:], noteBody, VKEKRaw)
	want := "95b9b4c1ff6900fb5b7981049ab4a6b017b1d917108b4db59762e3a82cad4210"
	if hex.EncodeToString(got) != want {
		t.Fatalf("Note digest (raw VKEK):\n got: %s\nwant: %s", hex.EncodeToString(got), want)
	}
}

func TestDigestPayloadVKEKBase64Golden(t *testing.T) {
	got := DigestPayload("MESSAGE", noteFrom, noteTo, noteCallID, goldenDate, goldenVKEK[:], noteBody, VKEKBase64String)
	want := "a16ab94a92cb27ff970ba37b882744fd831cde8829a3a0367def9d3fa437bfdc"
	if hex.EncodeToString(got) != want {
		t.Fatalf("Note digest (base64 VKEK):\n got: %s\nwant: %s", hex.EncodeToString(got), want)
	}
}

func TestBuildNoteHeaderGolden(t *testing.T) {
	got := BuildNoteHeader("MESSAGE", noteFrom, noteTo, noteCallID, goldenDate, goldenVKEK[:], noteBody, VKEKRaw)
	nonce := base64.StdEncoding.EncodeToString(decodeHex(t, "95b9b4c1ff6900fb5b7981049ab4a6b017b1d917108b4db59762e3a82cad4210"))
	want := `Digest nonce="` + nonce + `",algorithm=SM3`
	if got != want {
		t.Fatalf("Note header:\n got: %s\nwant: %s", got, want)
	}
}

func TestParseNoteHeader(t *testing.T) {
	nonce := base64.StdEncoding.EncodeToString(decodeHex(t, "95b9b4c1ff6900fb5b7981049ab4a6b017b1d917108b4db59762e3a82cad4210"))
	gotNonce, gotAlg, err := ParseNoteHeader(`Digest nonce="` + nonce + `",algorithm=SM3`)
	if err != nil {
		t.Fatalf("ParseNoteHeader: %v", err)
	}
	if gotNonce != nonce || gotAlg != "SM3" {
		t.Fatalf("parsed %q/%q", gotNonce, gotAlg)
	}
	if _, _, err := ParseNoteHeader(`Digest nonce="xx"`); err == nil {
		t.Fatal("Note without algorithm accepted")
	}
	if _, _, err := ParseNoteHeader(`Basic realm="x"`); err == nil {
		t.Fatal("non-Digest Note accepted")
	}
}

func TestVerifyNoteHeader(t *testing.T) {
	note := BuildNoteHeader("MESSAGE", noteFrom, noteTo, noteCallID, goldenDate, goldenVKEK[:], noteBody, VKEKRaw)
	if err := VerifyNoteHeader(note, "MESSAGE", noteFrom, noteTo, noteCallID, goldenDate, goldenVKEK[:], noteBody, VKEKRaw); err != nil {
		t.Fatalf("VerifyNoteHeader: %v", err)
	}
	// Tampered body must fail.
	if err := VerifyNoteHeader(note, "MESSAGE", noteFrom, noteTo, noteCallID, goldenDate, goldenVKEK[:], noteBody+"x", VKEKRaw); err == nil {
		t.Fatal("tampered body accepted")
	}
	// Wrong VKEK must fail.
	wrong := goldenVKEK
	wrong[0] ^= 0xFF
	if err := VerifyNoteHeader(note, "MESSAGE", noteFrom, noteTo, noteCallID, goldenDate, wrong[:], noteBody, VKEKRaw); err == nil {
		t.Fatal("wrong VKEK accepted")
	}
	// Encoding mismatch must fail.
	if err := VerifyNoteHeader(note, "MESSAGE", noteFrom, noteTo, noteCallID, goldenDate, goldenVKEK[:], noteBody, VKEKBase64String); err == nil {
		t.Fatal("VKEK encoding mismatch accepted")
	}
}

func TestFormatDate(t *testing.T) {
	got := FormatDate(time.Date(2024, 1, 31, 14, 40, 49, 583000000, time.UTC))
	if got != goldenDate {
		t.Fatalf("FormatDate = %q, want %q", got, goldenDate)
	}
}

func decodeHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("bad hex fixture: %v", err)
	}
	return b
}
