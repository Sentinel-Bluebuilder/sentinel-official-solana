package wfp

import (
	"fmt"
	"runtime"
	"unsafe"
)

//nolint:gochecknoglobals
var (
	LayerConnectV4GUID = layerALEAuthConnectV4
	LayerConnectV6GUID = layerALEAuthConnectV6
)

var (
	_ = [1]byte{}[unsafe.Offsetof(guid{}.data1)-0]
	_ = [1]byte{}[unsafe.Offsetof(guid{}.data2)-4]
	_ = [1]byte{}[unsafe.Offsetof(guid{}.data3)-6]
	_ = [1]byte{}[unsafe.Offsetof(guid{}.data4)-8]
	_ = [1]byte{}[unsafe.Offsetof(displayData{}.name)-0]
	_ = [1]byte{}[unsafe.Offsetof(displayData{}.description)-8]
	_ = [1]byte{}[unsafe.Offsetof(byteBlob{}.size)-0]
	_ = [1]byte{}[unsafe.Offsetof(byteBlob{}.data)-8]
	_ = [1]byte{}[unsafe.Offsetof(value{}.kind)-0]
	_ = [1]byte{}[unsafe.Offsetof(value{}.data)-8]
	_ = [1]byte{}[unsafe.Offsetof(filterCondition{}.fieldKey)-0]
	_ = [1]byte{}[unsafe.Offsetof(filterCondition{}.matchType)-16]
	_ = [1]byte{}[unsafe.Offsetof(filterCondition{}.value)-24]
	_ = [1]byte{}[unsafe.Offsetof(action{}.kind)-0]
	_ = [1]byte{}[unsafe.Offsetof(action{}.key)-4]
	_ = [1]byte{}[unsafe.Offsetof(sublayer{}.key)-0]
	_ = [1]byte{}[unsafe.Offsetof(sublayer{}.displayData)-16]
	_ = [1]byte{}[unsafe.Offsetof(sublayer{}.flags)-32]
	_ = [1]byte{}[unsafe.Offsetof(sublayer{}.providerKey)-40]
	_ = [1]byte{}[unsafe.Offsetof(sublayer{}.data)-48]
	_ = [1]byte{}[unsafe.Offsetof(sublayer{}.weight)-64]
	_ = [1]byte{}[unsafe.Offsetof(session{}.key)-0]
	_ = [1]byte{}[unsafe.Offsetof(session{}.displayData)-16]
	_ = [1]byte{}[unsafe.Offsetof(session{}.flags)-32]
	_ = [1]byte{}[unsafe.Offsetof(session{}.txnWaitMillis)-36]
	_ = [1]byte{}[unsafe.Offsetof(session{}.processID)-40]
	_ = [1]byte{}[unsafe.Offsetof(session{}.sid)-48]
	_ = [1]byte{}[unsafe.Offsetof(session{}.username)-56]
	_ = [1]byte{}[unsafe.Offsetof(session{}.kernelMode)-64]
	_ = [1]byte{}[unsafe.Offsetof(filter{}.key)-0]
	_ = [1]byte{}[unsafe.Offsetof(filter{}.displayData)-16]
	_ = [1]byte{}[unsafe.Offsetof(filter{}.flags)-32]
	_ = [1]byte{}[unsafe.Offsetof(filter{}.providerKey)-40]
	_ = [1]byte{}[unsafe.Offsetof(filter{}.data)-48]
	_ = [1]byte{}[unsafe.Offsetof(filter{}.layerKey)-64]
	_ = [1]byte{}[unsafe.Offsetof(filter{}.sublayerKey)-80]
	_ = [1]byte{}[unsafe.Offsetof(filter{}.weight)-96]
	_ = [1]byte{}[unsafe.Offsetof(filter{}.numConditions)-112]
	_ = [1]byte{}[unsafe.Offsetof(filter{}.conditions)-120]
	_ = [1]byte{}[unsafe.Offsetof(filter{}.action)-128]
	_ = [1]byte{}[unsafe.Offsetof(filter{}.context)-152]
	_ = [1]byte{}[unsafe.Offsetof(filter{}.reserved)-168]
	_ = [1]byte{}[unsafe.Offsetof(filter{}.id)-176]
	_ = [1]byte{}[unsafe.Offsetof(filter{}.effectiveWeight)-184]
	_ = [1]byte{}[unsafe.Offsetof(filterEnumTemplate{}.providerKey)-0]
	_ = [1]byte{}[unsafe.Offsetof(filterEnumTemplate{}.layerKey)-8]
	_ = [1]byte{}[unsafe.Offsetof(filterEnumTemplate{}.enumType)-24]
	_ = [1]byte{}[unsafe.Offsetof(filterEnumTemplate{}.flags)-28]
	_ = [1]byte{}[unsafe.Offsetof(filterEnumTemplate{}.contextTemplate)-32]
	_ = [1]byte{}[unsafe.Offsetof(filterEnumTemplate{}.numConditions)-40]
	_ = [1]byte{}[unsafe.Offsetof(filterEnumTemplate{}.conditions)-48]
	_ = [1]byte{}[unsafe.Offsetof(filterEnumTemplate{}.actionMask)-56]
	_ = [1]byte{}[unsafe.Offsetof(filterEnumTemplate{}.calloutKey)-64]
	_ = [1]byte{}[unsafe.Offsetof(v4AddrMask{}.addr)-0]
	_ = [1]byte{}[unsafe.Offsetof(v4AddrMask{}.mask)-4]
	_ = [1]byte{}[unsafe.Offsetof(v6AddrMask{}.addr)-0]
	_ = [1]byte{}[unsafe.Offsetof(v6AddrMask{}.prefixLength)-16]
)

type (
	ByteBlob           = byteBlob
	DisplayData        = displayData
	Filter             = filter
	FilterCondition    = filterCondition
	FilterEnumTemplate = filterEnumTemplate
	GUID               = guid
	NativeAction       = action
	Session            = session
	Sublayer           = sublayer
	V4AddrMask         = v4AddrMask
	V6AddrMask         = v6AddrMask
	Value              = value
)

type ConditionView struct {
	Bits     uint8
	Bytes    [16]byte
	Data     uint64
	Field    GUID
	Indirect uint64
	Kind     uint32
	Mask     uint32
	Match    uint32
}

type FilterView struct {
	Action     uint32
	Conditions []ConditionView
	Flags      uint32
	Layer      GUID
	Provider   *GUID
	Sublayer   GUID
	Weight     uint64
	WeightKind uint32
}

func CompileForTest(name string, rules []Rule) ([]FilterView, int, error) {
	set, err := compile(deriveSublayerKey(name), rules)
	if err != nil {
		return nil, 0, err
	}
	defer set.release()

	runtime.GC()

	views := make([]FilterView, 0, len(set.filters))
	for i := range set.filters {
		f := &set.filters[i]

		v := FilterView{Action: f.action.kind, Flags: f.flags, Layer: f.layerKey, Provider: f.providerKey,
			Sublayer: f.sublayerKey, Weight: f.weight.data, WeightKind: f.weight.kind}
		for _, c := range unsafe.Slice(f.conditions, f.numConditions) {
			cv := ConditionView{Data: c.value.data, Field: c.fieldKey, Kind: c.value.kind, Match: c.matchType}

			found := false

			for _, p := range set.keep {
				switch p := p.(type) {
				case *uint64:
					if pointerValue(unsafe.Pointer(p)) == cv.Data && cv.Kind == dataTypeUint64 {
						found = true
						cv.Indirect = *p
					}
				case *v4AddrMask:
					if pointerValue(unsafe.Pointer(p)) == cv.Data && cv.Kind == dataTypeV4AddrMask {
						found = true
						cv.Indirect = uint64(p.addr)
						cv.Mask = p.mask
					}
				case *[16]byte:
					if pointerValue(unsafe.Pointer(p)) == cv.Data && cv.Kind == dataTypeByteArray16 {
						found = true
						cv.Bytes = *p
					}
				case *v6AddrMask:
					if pointerValue(unsafe.Pointer(p)) == cv.Data && cv.Kind == dataTypeV6AddrMask {
						found = true
						cv.Bytes = p.addr
						cv.Bits = p.prefixLength
					}
				}
			}

			switch cv.Kind {
			case dataTypeUint64, dataTypeV4AddrMask, dataTypeByteArray16, dataTypeV6AddrMask:
				if !found {
					return nil, 0, fmt.Errorf("missing retained pointee for kind %d at %#x", cv.Kind, cv.Data)
				}
			}

			v.Conditions = append(v.Conditions, cv)
		}

		views = append(views, v)
	}

	return views, len(set.keep), nil
}

func ConditionGUIDs() []string {
	return []string{
		conditionFlags.String(),
		conditionIPLocalInterface.String(),
		conditionIPProtocol.String(),
		conditionIPRemoteAddress.String(),
		conditionIPRemotePort.String(),
	}
}

func DeriveSublayerKey(name string) GUID { return deriveSublayerKey(name) }

func NativeConstants() []uint32 {
	return []uint32{
		actionBlock,
		actionPermit,
		conditionFlagIsLoopback,
		dataTypeByteArray16,
		dataTypeUint8,
		dataTypeUint16,
		dataTypeUint32,
		dataTypeUint64,
		dataTypeV4AddrMask,
		dataTypeV6AddrMask,
		enumActionMaskAll,
		enumBatch,
		enumTypeOverlapping,
		filterFlagPersistent,
		sublayerFlagPersistent,
		matchEqual,
		matchFlagsAllSet,
		uint32(protocolTCP),
		uint32(protocolUDP),
		rpcAuthnWinNT,
		uint32(sublayerWeight),
		txnWaitTimeoutMillis,
		uint32(weightBlock),
		uint32(weightPermit),
	}
}

func NewDisplayDataForTest(name string) (*uint16, *uint16) {
	d := newDisplayData(name)

	return d.name, d.description
}

func NewEnumTemplateForTest(layer GUID) (*GUID, GUID, uint32, uint32) {
	t := newEnumTemplate(layer)

	return t.providerKey, t.layerKey, t.enumType, t.actionMask
}

func NewSessionForTest(name string) (uint32, uint32) {
	s := newSession(name)

	return s.flags, s.txnWaitMillis
}

func NewSublayerForTest(name string) (GUID, *GUID, uint32, uint16) {
	s := newSublayer(name)

	return s.key, s.providerKey, s.flags, s.weight
}

func UTF16Ptr(s string) *uint16 { return utf16Ptr(s) }
