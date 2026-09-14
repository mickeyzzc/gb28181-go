package sip

import (
	"testing"

	"github.com/ghettovoice/gosip/sip"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// GB/T 28181-2022 Annex H.3 platform-id-list codec: dash-separated 20-digit
// IDs with "200" at digits 11-13 (Annex E platform type). The example value
// below is the standard's own (H.3.1).
func TestParsePlatformIDList(t *testing.T) {
	golden := "65010000002000000001-65010200002000000001-65010205002000000001"
	ids, err := ParsePlatformIDList(golden)
	require.NoError(t, err)
	assert.Equal(t, []string{
		"65010000002000000001",
		"65010200002000000001",
		"65010205002000000001",
	}, ids)
	assert.Equal(t, golden, FormatPlatformIDList(ids))

	// Single-ID path (local channel anchored at this platform).
	ids, err = ParsePlatformIDList("34020000002000000001")
	require.NoError(t, err)
	assert.Len(t, ids, 1)

	// Empty and blank render/parse as empty.
	ids, err = ParsePlatformIDList("   ")
	require.NoError(t, err)
	assert.Empty(t, ids)
	assert.Equal(t, "", FormatPlatformIDList(nil))

	// Malformed entries are rejected by name: wrong type digits,
	// non-digits, wrong length.
	for _, bad := range []string{
		"34020000001320000001",  // 132 device type, not a platform
		"3402000000200000000",   // 19 digits
		"34020000002000000a01",  // non-digit
		"65010000002000000001-", // trailing separator
		"65010000002000000001-not-a-plugin",
	} {
		_, err := ParsePlatformIDList(bad)
		assert.Error(t, err, "value %q must be rejected", bad)
	}
}

func TestRoutePathHeader(t *testing.T) {
	assert.Nil(t, RoutePathHeader(""))
	h := RoutePathHeader("34020000002000000001")
	require.NotNil(t, h)
	gh, ok := h.(*sip.GenericHeader)
	require.True(t, ok)
	assert.Equal(t, "X-RoutePath", gh.HeaderName)
	assert.Equal(t, "34020000002000000001", gh.Contents)
}

// X-GB-Ver (Annex I): with Config.ProtocolVersion set, both REGISTER
// responses (401 challenge and 200 OK) carry the header; without it the
// wire form is unchanged.
func TestRegisterResponsesCarryXGBVer(t *testing.T) {
	cfg := testConfig(t)
	cfg.ProtocolVersion = "3.0"
	_, _ = startTestServer(t, cfg)
	client := newSIPClient(t, cfg.SIPListen)

	req := buildRequest(t, sip.REGISTER, testDeviceID, testServerID, cfg.SIPListen, client.localPort(), "")
	res := client.roundTrip(req)
	require.Equal(t, 401, int(res.StatusCode()))
	require.Len(t, res.GetHeaders(XGBVerHeaderName), 1, "401 must carry X-GB-Ver")
	assert.Equal(t, "3.0", res.GetHeaders(XGBVerHeaderName)[0].(*sip.GenericHeader).Contents)

	challenge := getChallenge(t, res)
	auth := digestAuth(t, challenge, req, cfg.Password)
	req2 := buildRequest(t, sip.REGISTER, testDeviceID, testServerID, cfg.SIPListen, client.localPort(), "", auth)
	res2 := client.roundTrip(req2)
	require.Equal(t, 200, int(res2.StatusCode()))
	require.Len(t, res2.GetHeaders(XGBVerHeaderName), 1, "200 OK must carry X-GB-Ver")
}

func TestRegisterResponsesOmitXGBVerByDefault(t *testing.T) {
	cfg := testConfig(t)
	_, _ = startTestServer(t, cfg)
	client := newSIPClient(t, cfg.SIPListen)

	req := buildRequest(t, sip.REGISTER, testDeviceID, testServerID, cfg.SIPListen, client.localPort(), "")
	res := client.roundTrip(req)
	require.Equal(t, 401, int(res.StatusCode()))
	assert.Empty(t, res.GetHeaders(XGBVerHeaderName), "unconfigured platform must not emit X-GB-Ver")
}
