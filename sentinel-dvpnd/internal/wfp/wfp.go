package wfp

import (
	"encoding/binary"
	"fmt"
	"unicode/utf16"

	"github.com/google/uuid"
)

const (
	actionBlock  uint32 = 0x1001
	actionPermit uint32 = 0x1002

	conditionFlagIsLoopback uint32 = 0x1

	dataTypeByteArray16 uint32 = 11
	dataTypeUint16      uint32 = 2
	dataTypeUint32      uint32 = 3
	dataTypeUint64      uint32 = 4
	dataTypeUint8       uint32 = 1
	dataTypeV4AddrMask  uint32 = 256
	dataTypeV6AddrMask  uint32 = 257

	enumActionMaskAll   uint32 = 0xFFFFFFFF
	enumBatch           uint32 = 64
	enumTypeOverlapping uint32 = 1

	filterFlagPersistent   uint32 = 0x1
	sublayerFlagPersistent uint32 = 0x1

	matchEqual       uint32 = 0
	matchFlagsAllSet uint32 = 6

	protocolTCP uint8 = 6
	protocolUDP uint8 = 17

	rpcAuthnWinNT        uint32 = 10
	sublayerWeight       uint16 = 0xFFF0
	txnWaitTimeoutMillis uint32 = 10000

	weightBlock  uint8 = 0
	weightPermit uint8 = 15
)

const displayDescription = "Sentinel dVPN kill switch"

//nolint:gochecknoglobals
var (
	conditionFlags = guid{
		data1: 0x632ce23b,
		data2: 0x5167,
		data3: 0x435c,
		data4: [8]byte{0x86, 0xd7, 0xe9, 0x03, 0x68, 0x4a, 0xa8, 0x0c},
	}
	conditionIPLocalInterface = guid{
		data1: 0x4cd62a49,
		data2: 0x59c3,
		data3: 0x4969,
		data4: [8]byte{0xb7, 0xf3, 0xbd, 0xa5, 0xd3, 0x28, 0x90, 0xa4},
	}
	conditionIPProtocol = guid{
		data1: 0x3971ef2b,
		data2: 0x623e,
		data3: 0x4f9a,
		data4: [8]byte{0x8c, 0xb1, 0x6e, 0x79, 0xb8, 0x06, 0xb9, 0xa7},
	}
	conditionIPRemoteAddress = guid{
		data1: 0xb235ae9a,
		data2: 0x1d64,
		data3: 0x49b8,
		data4: [8]byte{0xa4, 0x4c, 0x5f, 0xf3, 0xd9, 0x09, 0x50, 0x45},
	}
	conditionIPRemotePort = guid{
		data1: 0xc35a604d,
		data2: 0xd22b,
		data3: 0x4e1a,
		data4: [8]byte{0x91, 0xb4, 0x68, 0xf6, 0x74, 0xee, 0x67, 0x4b},
	}
	keyNamespace          = uuid.MustParse("4b311846-caf0-41ad-9d27-b4f97c6b14f4")
	layerALEAuthConnectV4 = guid{
		data1: 0xc38d57d1,
		data2: 0x05a7,
		data3: 0x4c33,
		data4: [8]byte{0x90, 0x4f, 0x7f, 0xbc, 0xee, 0xe6, 0x0e, 0x82},
	}
	layerALEAuthConnectV6 = guid{
		data1: 0x4a72393b,
		data2: 0x319f,
		data3: 0x44bc,
		data4: [8]byte{0x84, 0xc3, 0xba, 0x54, 0xdc, 0xb3, 0xb6, 0xb4},
	}
)

//nolint:decorder
type action struct {
	kind uint32
	key  guid
}

//nolint:decorder
type byteBlob struct {
	size uint32
	_    uint32
	data *byte
}

//nolint:decorder
type displayData struct {
	name        *uint16
	description *uint16
}

func newDisplayData(name string) displayData {
	return displayData{
		description: utf16Ptr(displayDescription),
		name:        utf16Ptr(name),
	}
}

//nolint:decorder
type filter struct {
	key             guid
	displayData     displayData
	flags           uint32
	_               uint32
	providerKey     *guid
	data            byteBlob
	layerKey        guid
	sublayerKey     guid
	weight          value
	numConditions   uint32
	_               uint32
	conditions      *filterCondition
	action          action
	_               uint32
	context         [2]uint64
	reserved        *guid
	id              uint64
	effectiveWeight value
}

//nolint:decorder
type filterCondition struct {
	fieldKey  guid
	matchType uint32
	_         uint32
	value     value
}

//nolint:decorder
type filterEnumTemplate struct {
	providerKey     *guid
	layerKey        guid
	enumType        uint32
	flags           uint32
	contextTemplate *byte
	numConditions   uint32
	_               uint32
	conditions      *filterCondition
	actionMask      uint32
	_               uint32
	calloutKey      *guid
}

type guid struct {
	data1 uint32
	data2 uint16
	data3 uint16
	data4 [8]byte
}

func (g guid) String() string {
	return fmt.Sprintf(
		"%08x-%04x-%04x-%02x%02x-%02x%02x%02x%02x%02x%02x",
		g.data1, g.data2, g.data3,
		g.data4[0], g.data4[1], g.data4[2], g.data4[3], g.data4[4], g.data4[5], g.data4[6], g.data4[7],
	)
}

//nolint:decorder
type session struct {
	key           guid
	displayData   displayData
	flags         uint32
	txnWaitMillis uint32
	processID     uint32
	_             uint32
	sid           *byte
	username      *uint16
	kernelMode    int32
	_             uint32
}

func newSession(name string) *session {
	return &session{
		displayData:   newDisplayData(name),
		txnWaitMillis: txnWaitTimeoutMillis,
	}
}

//nolint:decorder
type sublayer struct {
	key         guid
	displayData displayData
	flags       uint32
	_           uint32
	providerKey *guid
	data        byteBlob
	weight      uint16
	_           [6]byte
}

func newSublayer(name string) *sublayer {
	return &sublayer{
		displayData: newDisplayData(name),
		flags:       sublayerFlagPersistent,
		key:         deriveSublayerKey(name),
		weight:      sublayerWeight,
	}
}

type v4AddrMask struct {
	addr uint32
	mask uint32
}

type v6AddrMask struct {
	addr         [16]byte
	prefixLength uint8
}

//nolint:decorder
type value struct {
	kind uint32
	_    uint32
	data uint64
}

func deriveSublayerKey(name string) guid {
	id := uuid.NewSHA1(keyNamespace, []byte(name))

	return guid{
		data1: binary.BigEndian.Uint32(id[0:4]),
		data2: binary.BigEndian.Uint16(id[4:6]),
		data3: binary.BigEndian.Uint16(id[6:8]),
		data4: [8]byte(id[8:16]),
	}
}

func newEnumTemplate(layer guid) *filterEnumTemplate {
	return &filterEnumTemplate{
		actionMask: enumActionMaskAll,
		enumType:   enumTypeOverlapping,
		layerKey:   layer,
	}
}

func utf16Ptr(s string) *uint16 {
	buf := utf16.Encode([]rune(s))
	buf = append(buf, 0)

	return &buf[0]
}
