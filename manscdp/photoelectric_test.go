package manscdp

import (
	"encoding/xml"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// GB/T 28181-2022 Catalog Item PhotoelectricImagingType (光电成像类型):
// 1 = visible light, 2 = thermal imaging — dual-spectrum devices mark their
// thermal channel so a platform can tell it from the visible-light sibling
// without probing. Further values are reserved by the standard and must pass
// through verbatim. 0/absent = unspecified.

func TestItemPhotoelectricImagingTypeEncodeGolden(t *testing.T) {
	// omitempty contract: an unset (0) value must NOT alter the wire bytes —
	// emitters built before this field stay byte-identical. The element
	// order contract: the marking sits after PTZType, last in the Item.
	out, err := xml.Marshal(Item{DeviceID: "34020000001320000001", Name: "ch1", Status: "ON"})
	require.NoError(t, err)
	assert.NotContains(t, string(out), "PhotoelectricImagingType",
		"unset marking must not alter pre-2022 wire bytes")
	assert.True(t, strings.HasSuffix(string(out), "<PTZType>0</PTZType></Item>"),
		"unmarked item must end at PTZType; got %s", out)

	out, err = xml.Marshal(Item{
		DeviceID: "34020000001320000002", Name: "thermal", Status: "ON",
		PhotoelectricImagingType: 2,
	})
	require.NoError(t, err)
	assert.True(t, strings.HasSuffix(string(out),
		"<PTZType>0</PTZType><PhotoelectricImagingType>2</PhotoelectricImagingType></Item>"),
		"thermal marking rides after PTZType; got %s", out)
}

func TestCatalogDecodePhotoelectricImagingType(t *testing.T) {
	data := []byte("<Response><CmdType>Catalog</CmdType><SN>3</SN>" +
		"<DeviceID>34020000001110000001</DeviceID><SumNum>3</SumNum>" +
		`<DeviceList Num="3">` +
		"<Item><DeviceID>34020000001320000001</DeviceID><Name>visible</Name><Status>ON</Status></Item>" +
		"<Item><DeviceID>34020000001320000002</DeviceID><Name>thermal</Name><Status>ON</Status>" +
		"<PhotoelectricImagingType>2</PhotoelectricImagingType></Item>" +
		"<Item><DeviceID>34020000001320000003</DeviceID><Name>reserved</Name><Status>ON</Status>" +
		"<PhotoelectricImagingType>9</PhotoelectricImagingType></Item>" +
		"</DeviceList></Response>")
	ct, v, err := Decode(data)
	require.NoError(t, err)
	assert.Equal(t, CmdCatalog, ct)
	cat := v.(Catalog)
	require.Len(t, cat.Item, 3)

	assert.Zero(t, cat.Item[0].PhotoelectricImagingType, "absent element = unspecified")
	assert.Equal(t, 2, cat.Item[1].PhotoelectricImagingType)
	assert.Equal(t, 9, cat.Item[2].PhotoelectricImagingType,
		"values reserved by the standard pass through verbatim — no enum validation")

	// Re-encode the decoded value byte-identically (round-trip contract).
	re, err := xml.Marshal(cat)
	require.NoError(t, err)
	_, v2, err := Decode(append(append([]byte{}, xmlHeader...), re...))
	require.NoError(t, err)
	re2, err := xml.Marshal(v2)
	require.NoError(t, err)
	assert.Equal(t, string(re), string(re2), "decoded value must re-encode byte-identically")
}
