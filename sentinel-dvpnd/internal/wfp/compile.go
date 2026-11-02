package wfp

import (
	"encoding/binary"
	"fmt"
	"net/netip"
	"runtime"
	"unsafe"
)

type filterSet struct {
	filters     []filter
	keep        []any
	pins        runtime.Pinner
	sublayerKey guid
}

func (s *filterSet) compileCondition(c Condition) (filterCondition, error) {
	switch c.Field {
	case FieldFlags:
		return filterCondition{
			fieldKey:  conditionFlags,
			matchType: matchFlagsAllSet,
			value: value{
				kind: dataTypeUint32,
				data: uint64(c.Flags),
			},
		}, nil
	case FieldLocalInterface:
		luid := new(uint64)
		*luid = c.InterfaceLUID
		s.retain(luid)

		return filterCondition{
			fieldKey:  conditionIPLocalInterface,
			matchType: matchEqual,
			value: value{
				kind: dataTypeUint64,
				data: pointerValue(unsafe.Pointer(luid)),
			},
		}, nil
	case FieldProtocol:
		return filterCondition{
			fieldKey:  conditionIPProtocol,
			matchType: matchEqual,
			value: value{
				kind: dataTypeUint8,
				data: uint64(c.Protocol),
			},
		}, nil
	case FieldRemoteAddress:
		return s.remoteAddress(c.Prefix)
	case FieldRemotePort:
		return filterCondition{
			fieldKey:  conditionIPRemotePort,
			matchType: matchEqual,
			value: value{
				kind: dataTypeUint16,
				data: uint64(c.Port),
			},
		}, nil
	default:
		return filterCondition{}, fmt.Errorf("%w: field %d", ErrInvalidRule, c.Field)
	}
}

func (s *filterSet) compileRule(r Rule) (filter, error) {
	if r.Action != ActionBlock && r.Action != ActionPermit {
		return filter{}, fmt.Errorf("%w: action %d", ErrInvalidRule, r.Action)
	}

	if r.Layer != LayerConnectV4 && r.Layer != LayerConnectV6 {
		return filter{}, fmt.Errorf("%w: layer %d", ErrInvalidRule, r.Layer)
	}

	if r.Weight > 15 {
		return filter{}, fmt.Errorf("%w: weight %d", ErrInvalidRule, r.Weight)
	}

	conditions := make([]filterCondition, 0, len(r.Conditions))

	for _, c := range r.Conditions {
		if c.Field == FieldRemoteAddress && (c.Prefix.Addr().Is4In6() || addrLayer(c.Prefix.Addr()) != r.Layer) {
			return filter{}, fmt.Errorf("%w: prefix %s for layer %d", ErrInvalidRule, c.Prefix, r.Layer)
		}

		fc, err := s.compileCondition(c)
		if err != nil {
			return filter{}, err
		}

		conditions = append(conditions, fc)
	}

	name := utf16Ptr(r.Name)
	s.retain(name)

	f := filter{
		action:        action{kind: actionKind(r.Action)},
		displayData:   displayData{name: name},
		flags:         filterFlagPersistent,
		layerKey:      layerKey(r.Layer),
		numConditions: uint32(len(conditions)),
		providerKey:   nil,
		sublayerKey:   s.sublayerKey,
		weight: value{
			kind: dataTypeUint8,
			data: uint64(r.Weight),
		},
	}

	if len(conditions) > 0 {
		s.retain(&conditions[0])
		f.conditions = &conditions[0]
	}

	return f, nil
}

func (s *filterSet) release() {
	s.pins.Unpin()
	s.filters = nil
	s.keep = nil
}

func (s *filterSet) remoteAddress(p netip.Prefix) (filterCondition, error) {
	if !p.IsValid() {
		return filterCondition{}, fmt.Errorf("%w: invalid prefix %s", ErrInvalidRule, p)
	}

	p = p.Masked()
	addr := p.Addr()
	cond := filterCondition{
		fieldKey:  conditionIPRemoteAddress,
		matchType: matchEqual,
	}

	switch {
	case addr.Is4() && p.Bits() == addr.BitLen():
		cond.value = value{
			kind: dataTypeUint32,
			data: uint64(binary.BigEndian.Uint32(addr.AsSlice())),
		}
	case addr.Is4():
		m := &v4AddrMask{
			addr: binary.BigEndian.Uint32(addr.AsSlice()),
			mask: maskV4(p.Bits()),
		}
		s.retain(m)
		cond.value = value{
			kind: dataTypeV4AddrMask,
			data: pointerValue(unsafe.Pointer(m)),
		}
	case p.Bits() == addr.BitLen():
		b := new([16]byte)
		*b = addr.As16()
		s.retain(b)
		cond.value = value{
			kind: dataTypeByteArray16,
			data: pointerValue(unsafe.Pointer(b)),
		}
	default:
		m := &v6AddrMask{
			addr:         addr.As16(),
			prefixLength: uint8(p.Bits()),
		}
		s.retain(m)
		cond.value = value{
			kind: dataTypeV6AddrMask,
			data: pointerValue(unsafe.Pointer(m)),
		}
	}

	return cond, nil
}

func (s *filterSet) retain(p any) {
	s.pins.Pin(p)
	s.keep = append(s.keep, p)
}

func actionKind(a Action) uint32 {
	if a == ActionPermit {
		return actionPermit
	}

	return actionBlock
}

func compile(sublayerKey guid, rules []Rule) (*filterSet, error) {
	set := &filterSet{
		filters:     make([]filter, 0, len(rules)),
		sublayerKey: sublayerKey,
	}

	for _, r := range rules {
		f, err := set.compileRule(r)
		if err != nil {
			set.release()

			return nil, err
		}

		set.filters = append(set.filters, f)
	}

	if len(set.filters) > 0 {
		set.retain(&set.filters[0])
	}

	return set, nil
}

func layerKey(l Layer) guid {
	if l == LayerConnectV6 {
		return layerALEAuthConnectV6
	}

	return layerALEAuthConnectV4
}

func maskV4(bits int) uint32 {
	if bits == 0 {
		return 0
	}

	return ^uint32(0) << (32 - bits)
}

func pointerValue(p unsafe.Pointer) uint64 { return uint64(uintptr(p)) }
