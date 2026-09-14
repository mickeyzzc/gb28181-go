package sip

import (
	"fmt"
	"strings"

	"github.com/ghettovoice/gosip/sip"
)

// GB/T 28181-2022 signaling additions: the protocol-version header
// (Annex I) and the multi-level-cascade path headers (Annex H.3).

// XGBVerHeaderName is the REGISTER protocol-version header (Annex I):
// "X-GB-Ver: 3.0" identifies GB/T 28181-2022 (1.0=2011, 1.1=2011 amend,
// 2.0=2016, 3.0=2022). Both sides learn the peer's version during
// registration so the higher side can avoid messages the lower one cannot
// parse.
const XGBVerHeaderName = "X-GB-Ver"

// xGBVerHeader builds the optional version header; nil when v is empty
// (the library never forces the header onto the wire — hosts opt in).
func xGBVerHeader(v string) sip.Header {
	if v == "" {
		return nil
	}
	return &sip.GenericHeader{HeaderName: XGBVerHeaderName, Contents: v}
}

// appendXGBVer appends the version header to headers when configured.
func appendXGBVer(headers []sip.Header, v string) []sip.Header {
	if h := xGBVerHeader(v); h != nil {
		return append(headers, h)
	}
	return headers
}

// Platform ID list codec (Annex H.3): X-RoutePath (INVITE responses) and
// X-PreferredPath (INVITE requests) carry dash-separated 20-digit platform
// IDs whose digits 11-13 are "200" (Annex E platform type). Examples:
//
//	X-RoutePath: 65010000002000000001-65010200002000000001-65010205002000000001

// ParsePlatformIDList splits a path header value into its platform IDs.
// Every ID must be 20 digits with "200" at digits 11-13; anything else is
// an error naming the offender.
func ParsePlatformIDList(v string) ([]string, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil, nil
	}
	parts := strings.Split(v, "-")
	ids := make([]string, 0, len(parts))
	for _, p := range parts {
		if !validPlatformID(p) {
			return nil, fmt.Errorf("invalid platform ID %q", p)
		}
		ids = append(ids, p)
	}
	return ids, nil
}

// FormatPlatformIDList renders IDs back into the dash-separated form;
// empty input renders "".
func FormatPlatformIDList(ids []string) string {
	return strings.Join(ids, "-")
}

// validPlatformID checks the Annex E platform-ID shape used by the path
// headers: 20 digits, digits 11-13 = "200".
func validPlatformID(id string) bool {
	if len(id) != 20 {
		return false
	}
	for _, c := range id {
		if c < '0' || c > '9' {
			return false
		}
	}
	return id[10:13] == "200"
}

// RoutePathHeaderName / PreferredPathHeaderName (Annex H.3).
const (
	RoutePathHeaderName     = "X-RoutePath"
	PreferredPathHeaderName = "X-PreferredPath"
)

// RoutePathHeader builds the X-RoutePath response header announcing this
// platform's ID (a middle platform prepends its own ID when forwarding a
// lower domain's response upward); nil when announce is empty. Used by the
// cascade package and available to hosts answering INVITEs directly.
func RoutePathHeader(announce string) sip.Header {
	if announce == "" {
		return nil
	}
	return &sip.GenericHeader{HeaderName: RoutePathHeaderName, Contents: announce}
}
