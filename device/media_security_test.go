package device

import "testing"

// SDPSecurityAttributeValue pins the tag-free "a=security:" extraction the
// INVITE paths consult: these tests run in the default build (no gb35114
// tag), pinning that the seam carries zero national-cryptography code and
// behaves identically with and without the tag.
func TestSDPSecurityAttributeValue(t *testing.T) {
	cases := []struct {
		name string
		sdp  string
		want string
	}{
		{"absent", "v=0\r\ns=Play\r\nm=video 30000 RTP/AVP 96\r\n", ""},
		{"present-crlf", "v=0\r\na=security:1\r\nm=video 30000 RTP/AVP 96\r\n", "1"},
		{"present-lf", "v=0\na=security:2\n", "2"},
		{"trims-space", "a=security:  3  \r\n", "3"},
		{"empty-value-skipped", "a=security:\r\na=security:4\r\n", "4"},
		{"prefix-is-not-match", "a=securityfoo:5\r\n", ""},
		{"first-wins", "a=security:6\r\na=security:7\r\n", "6"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := SDPSecurityAttributeValue(tc.sdp); got != tc.want {
				t.Fatalf("SDPSecurityAttributeValue(%q) = %q, want %q", tc.sdp, got, tc.want)
			}
		})
	}
}
