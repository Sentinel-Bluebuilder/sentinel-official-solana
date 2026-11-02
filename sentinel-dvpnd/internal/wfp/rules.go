package wfp

import (
	"errors"
	"fmt"
	"net/netip"
)

const (
	ActionBlock Action = iota + 1
	ActionPermit
)

const (
	FieldFlags Field = iota + 1
	FieldLocalInterface
	FieldProtocol
	FieldRemoteAddress
	FieldRemotePort
)

const (
	LayerConnectV4 Layer = iota + 1
	LayerConnectV6
)

var ErrInvalidRule = errors.New("invalid rule")

//nolint:gochecknoglobals // Immutable prefix tables.
var (
	dhcpPortsV4 = []uint16{67, 68}
	dhcpPortsV6 = []uint16{546, 547}
	lanPrefixes = []netip.Prefix{
		netip.MustParsePrefix("10.0.0.0/8"),
		netip.MustParsePrefix("172.16.0.0/12"),
		netip.MustParsePrefix("192.168.0.0/16"),
	}
	linkLocalV4 = netip.MustParsePrefix("169.254.0.0/16")
	linkLocalV6 = netip.MustParsePrefix("fe80::/10")
)

type Action uint8

type Condition struct {
	Field         Field
	Flags         uint32
	InterfaceLUID uint64
	Port          uint16
	Prefix        netip.Prefix
	Protocol      uint8
}

type Field uint8

type Layer uint8

type Rule struct {
	Action     Action
	Conditions []Condition
	Layer      Layer
	Name       string
	Weight     uint8
}

func NewRules(tunnelLUID uint64, nodeAddrs []netip.Addr, rpcAndWSAddrs []netip.AddrPort, allowLAN bool) []Rule {
	var rules []Rule

	for _, layer := range []Layer{LayerConnectV4, LayerConnectV6} {
		rules = append(
			rules,
			Rule{
				Action: ActionBlock,
				Layer:  layer,
				Name:   "block all",
				Weight: weightBlock,
			},
		)
		rules = append(
			rules,
			permit(layer, "loopback", Condition{
				Field: FieldFlags,
				Flags: conditionFlagIsLoopback,
			}),
		)

		if tunnelLUID != 0 {
			rules = append(
				rules,
				permit(layer, "tunnel", Condition{
					Field:         FieldLocalInterface,
					InterfaceLUID: tunnelLUID,
				}),
			)
		}
	}

	for _, addr := range nodeAddrs {
		addr = addr.Unmap()
		rules = append(rules, permit(addrLayer(addr), "node "+addr.String(), hostCondition(addr)))
	}

	for _, ap := range rpcAndWSAddrs {
		addr := ap.Addr().Unmap()
		rules = append(
			rules,
			permit(addrLayer(addr), "rpc "+netip.AddrPortFrom(addr, ap.Port()).String(), Condition{
				Field:    FieldProtocol,
				Protocol: protocolTCP,
			},
				hostCondition(addr),
				Condition{
					Field: FieldRemotePort,
					Port:  ap.Port(),
				}),
		)
	}

	for _, port := range dhcpPortsV4 {
		rules = append(rules, permit(LayerConnectV4, fmt.Sprintf("dhcp %d", port), udpPort(port)...))
	}

	for _, port := range dhcpPortsV6 {
		rules = append(rules, permit(LayerConnectV6, fmt.Sprintf("dhcp %d", port), udpPort(port)...))
	}

	rules = append(
		rules,
		permit(LayerConnectV4, "link-local", Condition{
			Field:  FieldRemoteAddress,
			Prefix: linkLocalV4,
		}),
	)
	rules = append(
		rules,
		permit(LayerConnectV6, "link-local", Condition{
			Field:  FieldRemoteAddress,
			Prefix: linkLocalV6,
		}),
	)

	if allowLAN {
		for _, p := range lanPrefixes {
			rules = append(
				rules,
				permit(LayerConnectV4, "lan "+p.String(), Condition{
					Field:  FieldRemoteAddress,
					Prefix: p,
				}),
			)
		}
	}

	return rules
}

func addrLayer(addr netip.Addr) Layer {
	if addr.Is4() {
		return LayerConnectV4
	}

	return LayerConnectV6
}

func hostCondition(addr netip.Addr) Condition {
	return Condition{
		Field:  FieldRemoteAddress,
		Prefix: netip.PrefixFrom(addr, addr.BitLen()),
	}
}

func permit(layer Layer, name string, conditions ...Condition) Rule {
	return Rule{
		Action:     ActionPermit,
		Conditions: conditions,
		Layer:      layer,
		Name:       name,
		Weight:     weightPermit,
	}
}

func udpPort(port uint16) []Condition {
	return []Condition{
		{
			Field:    FieldProtocol,
			Protocol: protocolUDP,
		},
		{
			Field: FieldRemotePort,
			Port:  port,
		},
	}
}
