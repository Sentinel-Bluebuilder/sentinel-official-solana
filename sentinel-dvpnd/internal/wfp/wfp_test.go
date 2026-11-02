package wfp_test

import (
	"runtime"
	"testing"
	"unicode/utf16"
	"unsafe"

	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-dvpnd/internal/wfp"
)

var (
	_ = [1]byte{}[unsafe.Sizeof(wfp.GUID{})-16]
	_ = [1]byte{}[unsafe.Sizeof(wfp.DisplayData{})-16]
	_ = [1]byte{}[unsafe.Sizeof(wfp.ByteBlob{})-16]
	_ = [1]byte{}[unsafe.Sizeof(wfp.Value{})-16]
	_ = [1]byte{}[unsafe.Sizeof(wfp.FilterCondition{})-40]
	_ = [1]byte{}[unsafe.Sizeof(wfp.NativeAction{})-20]
	_ = [1]byte{}[unsafe.Sizeof(wfp.Sublayer{})-72]
	_ = [1]byte{}[unsafe.Sizeof(wfp.Session{})-72]
	_ = [1]byte{}[unsafe.Sizeof(wfp.Filter{})-200]
	_ = [1]byte{}[unsafe.Sizeof(wfp.FilterEnumTemplate{})-72]
	_ = [1]byte{}[unsafe.Sizeof(wfp.V4AddrMask{})-8]
	_ = [1]byte{}[unsafe.Sizeof(wfp.V6AddrMask{})-17]
)

func TestConditionGUIDs(t *testing.T) {
	t.Parallel()

	require.Equal(t, []string{
		"632ce23b-5167-435c-86d7-e903684aa80c",
		"4cd62a49-59c3-4969-b7f3-bda5d32890a4",
		"3971ef2b-623e-4f9a-8cb1-6e79b806b9a7",
		"b235ae9a-1d64-49b8-a44c-5ff3d9095045",
		"c35a604d-d22b-4e1a-91b4-68f674ee674b",
	}, wfp.ConditionGUIDs())
}

func TestDeriveSublayerKey(t *testing.T) {
	t.Parallel()

	require.Equal(t, "088bc095-2927-5823-a1ae-d982d649ac09", wfp.DeriveSublayerKey("sentinel-dvpn-c0").String())
	require.Equal(t, "c60d763a-c307-50b5-a349-406f8f160d14", wfp.DeriveSublayerKey("sentinel-dvpn-c1").String())
}

func TestGUID_String(t *testing.T) {
	t.Parallel()

	require.Equal(t, "c38d57d1-05a7-4c33-904f-7fbceee60e82", wfp.LayerConnectV4GUID.String())
	require.Equal(t, "4a72393b-319f-44bc-84c3-ba54dcb3b6b4", wfp.LayerConnectV6GUID.String())
}

func TestNativeConstants(t *testing.T) {
	t.Parallel()

	require.Equal(t, []uint32{
		0x1001, 0x1002, 1, 11, 1, 2, 3, 4, 256, 257,
		0xffffffff, 64, 1, 1, 1, 0, 6, 6, 17, 10, 0xfff0, 10000, 0, 15,
	}, wfp.NativeConstants())
}

func TestNewDisplayData(t *testing.T) {
	t.Parallel()

	name, description := wfp.NewDisplayDataForTest("sentinel-dvpn-c0")

	runtime.GC()

	require.Equal(t, append(utf16.Encode([]rune("sentinel-dvpn-c0")), 0), unsafe.Slice(name, 17))
	require.Equal(t, append(utf16.Encode([]rune("Sentinel dVPN kill switch")), 0), unsafe.Slice(description, 26))
}

func TestNewEnumTemplate(t *testing.T) {
	t.Parallel()

	providerKey, layer, enumType, mask := wfp.NewEnumTemplateForTest(wfp.LayerConnectV6GUID)
	require.Nil(t, providerKey)
	require.Equal(t, wfp.LayerConnectV6GUID, layer)
	require.EqualValues(t, 1, enumType)
	require.EqualValues(t, 0xFFFFFFFF, mask)
}

func TestNewSession(t *testing.T) {
	t.Parallel()

	flags, wait := wfp.NewSessionForTest("sentinel-dvpn-c0")
	require.Zero(t, flags)
	require.EqualValues(t, 10000, wait)
}

func TestNewSublayer(t *testing.T) {
	t.Parallel()

	key, providerKey, flags, weight := wfp.NewSublayerForTest("sentinel-dvpn-c0")
	require.Equal(t, wfp.DeriveSublayerKey("sentinel-dvpn-c0"), key)
	require.Nil(t, providerKey)
	require.EqualValues(t, 1, flags)
	require.EqualValues(t, 0xFFF0, weight)
}

func TestUTF16Ptr(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		text string
		want []uint16
	}{
		{name: "empty", want: []uint16{0}},
		{name: "ascii", text: "ab", want: []uint16{'a', 'b', 0}},
		{name: "unicode", text: "é😀", want: []uint16{0xe9, 0xd83d, 0xde00, 0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			p := wfp.UTF16Ptr(tc.text)

			runtime.GC()

			require.Equal(t, tc.want, unsafe.Slice(p, len(tc.want)))
		})
	}
}
