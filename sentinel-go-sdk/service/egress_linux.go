package service

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"strings"
	"text/template"

	"github.com/vishvananda/netlink"
)

const (
	nftBinary       = "nft"
	rulesetTemplate = `add table inet {{ .Table }}
delete table inet {{ .Table }}
table inet {{ .Table }} {
  chain forward {
    type filter hook forward priority 0; policy accept;
    iifname "{{ .Iface }}" accept
    oifname "{{ .Iface }}" ct state established,related accept
  }
  chain postrouting {
    type nat hook postrouting priority srcnat; policy accept;
{{ range .Prefixes }}    {{ .Family }} saddr {{ .Prefix }} oifname "{{ $.OutIface }}" masquerade
{{ end }}  }
}
`
)

var ErrDefaultRouteNotFound = errors.New("default route not found")

type Egress interface {
	Teardown(ctx context.Context) error
}

var _ Egress = (*nftEgress)(nil)

type nftEgress struct {
	table string
}

func (e *nftEgress) Teardown(ctx context.Context) error {
	cmd := Command{
		Args: []string{"delete", "table", "inet", e.table},
		Path: nftBinary,
	}

	if _, err := Run(ctx, cmd); err != nil {
		return fmt.Errorf("deleting table %s: %w", e.table, err)
	}

	return nil
}

type rulesetPrefix struct {
	Family string
	Prefix string
}

type rulesetTemplateData struct {
	Iface    string
	OutIface string
	Prefixes []rulesetPrefix
	Table    string
}

func DefaultRouteInterface() (string, error) {
	routes, err := netlink.RouteGet(net.ParseIP("1.1.1.1"))
	if err == nil && len(routes) > 0 {
		if link, err := netlink.LinkByIndex(routes[0].LinkIndex); err == nil {
			return link.Attrs().Name, nil
		}
	}

	list, err := netlink.RouteList(nil, netlink.FAMILY_V4)
	if err != nil {
		return "", fmt.Errorf("listing routes: %w", err)
	}

	for _, route := range list {
		if route.Dst != nil {
			continue
		}

		link, err := netlink.LinkByIndex(route.LinkIndex)
		if err != nil {
			continue
		}

		return link.Attrs().Name, nil
	}

	return "", ErrDefaultRouteNotFound
}

func RenderRuleset(table, iface, outIface string, prefixes []netip.Prefix) string {
	data := rulesetTemplateData{
		Iface:    iface,
		OutIface: outIface,
		Table:    table,
	}

	for _, prefix := range prefixes {
		family := "ip"
		if prefix.Addr().Is6() {
			family = "ip6"
		}

		data.Prefixes = append(data.Prefixes, rulesetPrefix{
			Family: family,
			Prefix: prefix.String(),
		})
	}

	var builder strings.Builder

	tmpl := template.Must(template.New("ruleset").Parse(rulesetTemplate))
	if err := tmpl.Execute(&builder, data); err != nil {
		panic(err)
	}

	return builder.String()
}

func SetupEgress(ctx context.Context, table, iface, outIface string, prefixes []netip.Prefix) (Egress, error) {
	if outIface == "" {
		def, err := DefaultRouteInterface()
		if err != nil {
			return nil, err
		}

		outIface = def
	}

	if err := os.WriteFile("/proc/sys/net/ipv4/ip_forward", []byte("1"), 0o644); err != nil { //#nosec G306
		return nil, fmt.Errorf("enabling ipv4 forwarding: %w", err)
	}

	hasV6 := false

	for _, prefix := range prefixes {
		if prefix.Addr().Is6() {
			hasV6 = true
		}
	}

	if hasV6 {
		if err := os.WriteFile("/proc/sys/net/ipv6/conf/all/forwarding", []byte("1"), 0o644); err != nil { //#nosec G306
			return nil, fmt.Errorf("enabling ipv6 forwarding: %w", err)
		}
	}

	cmd := Command{
		Args:  []string{"-f", "-"},
		Path:  nftBinary,
		Stdin: strings.NewReader(RenderRuleset(table, iface, outIface, prefixes)),
	}

	if _, err := Run(ctx, cmd); err != nil {
		return nil, fmt.Errorf("loading nft ruleset: %w", err)
	}

	return &nftEgress{table: table}, nil
}
