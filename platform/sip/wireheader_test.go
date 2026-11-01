package sip

import (
	"testing"

	"github.com/ghettovoice/gosip/sip"
	"github.com/stretchr/testify/require"
)

// wireHeaderValue backs the GB35114 cascade-adjacent request routing (and
// any header lookup on inbound requests): the raw-header fast path first,
// then gosip's typed headers. A request whose header goes through the
// typed path only must still resolve.
func TestWireHeaderValueTypedAndRawPaths(t *testing.T) {
	req := buildRequest(t, sip.MESSAGE, testDeviceID, testServerID, "127.0.0.1:5060", 5060, "")
	require.NotEmpty(t, wireHeaderValue(req, "Call-ID"), "typed Call-ID must resolve")
	require.Empty(t, wireHeaderValue(req, "Authorization"), "absent header must be empty")

	// A generic (raw) header gosip does not type natively.
	req.AppendHeader(&sip.GenericHeader{HeaderName: "X-Probe", Contents: "value-1"})
	require.Equal(t, "value-1", wireHeaderValue(req, "X-Probe"))
}
