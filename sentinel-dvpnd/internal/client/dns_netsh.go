package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"

	"github.com/sentinel-official/sentinel-dvpnd/internal/config"
)

const (
	ipconfigBinary      = "ipconfig"
	netshBinary         = "netsh"
	netshIPv4           = "ipv4"
	netshIPv6           = "ipv6"
	netshMarkerFileName = "netsh-dns.json"
)

type netshBackend struct {
	markerPath string
	run        runFunc
}

func (b netshBackend) Apply(ctx context.Context, iface string, servers []string) error {
	v4, v6, err := splitFamilies(servers)
	if err != nil {
		return err
	}

	if err := b.Cleanup(ctx); err != nil {
		return err
	}

	if err := b.writeMarker(iface); err != nil {
		return err
	}

	if err := b.set(ctx, netshIPv4, iface, v4); err != nil {
		return err
	}

	if err := b.set(ctx, netshIPv6, iface, v6); err != nil {
		return err
	}

	return b.flush(ctx)
}

func (b netshBackend) Cleanup(ctx context.Context) error {
	iface, err := b.readMarker()
	if err != nil || iface == "" {
		return err
	}

	return b.Revert(ctx, iface)
}

func (b netshBackend) Revert(ctx context.Context, iface string) error {
	marked, err := b.readMarker()
	if err != nil {
		return err
	}

	if marked == "" {
		return nil
	}

	if marked != iface {
		return fmt.Errorf("netsh revert %q: marker belongs to %q", iface, marked)
	}

	present, err := interfaceExists(iface)
	if err != nil {
		return fmt.Errorf("netsh revert: %w", err)
	}

	if present {
		for _, family := range []string{netshIPv4, netshIPv6} {
			args := []string{"interface", family, "set", "dnsservers", "name=" + iface, "source=dhcp"}
			if out, err := b.run(ctx, "", netshBinary, args...); err != nil {
				return fmt.Errorf("netsh %s set dnsservers dhcp: %w: %s", family, err, out)
			}
		}
	}

	if err := b.flush(ctx); err != nil {
		return err
	}

	return b.clearMarker(iface)
}

func (b netshBackend) clearMarker(iface string) error {
	marked, err := b.readMarker()
	if err != nil || marked != iface {
		return err
	}

	if err := os.Remove(b.markerPath); err != nil {
		return fmt.Errorf("removing netsh dns marker: %w", err)
	}

	return nil
}

func (b netshBackend) flush(ctx context.Context) error {
	if out, err := b.run(ctx, "", ipconfigBinary, "/flushdns"); err != nil {
		return fmt.Errorf("ipconfig /flushdns: %w: %s", err, out)
	}

	return nil
}

func (b netshBackend) readMarker() (string, error) {
	raw, err := os.ReadFile(b.markerPath)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}

	if err != nil {
		return "", fmt.Errorf("reading netsh dns marker: %w", err)
	}

	var marker netshMarker
	if err := json.Unmarshal(raw, &marker); err != nil {
		return "", fmt.Errorf("decoding netsh dns marker: %w", err)
	}

	if marker.Iface == "" {
		return "", errors.New("decoding netsh dns marker: empty interface")
	}

	return marker.Iface, nil
}

func (b netshBackend) set(ctx context.Context, family, iface string, servers []string) error {
	if len(servers) == 0 {
		return nil
	}

	args := []string{
		"interface", family, "set", "dnsservers",
		"name=" + iface, "source=static", "address=" + servers[0], "register=none", "validate=no",
	}
	if out, err := b.run(ctx, "", netshBinary, args...); err != nil {
		return fmt.Errorf("netsh %s set dnsservers: %w: %s", family, err, out)
	}

	for i, server := range servers[1:] {
		args := []string{
			"interface", family, "add", "dnsservers",
			"name=" + iface, "address=" + server, "index=" + strconv.Itoa(i+2), "validate=no",
		}
		if out, err := b.run(ctx, "", netshBinary, args...); err != nil {
			return fmt.Errorf("netsh %s add dnsservers: %w: %s", family, err, out)
		}
	}

	return nil
}

func (b netshBackend) writeMarker(iface string) error {
	if iface == "" {
		return errors.New("writing netsh dns marker: empty interface")
	}

	raw, err := json.Marshal(netshMarker{Iface: iface})
	if err != nil {
		return fmt.Errorf("encoding netsh dns marker: %w", err)
	}

	temp, err := os.CreateTemp(filepath.Dir(b.markerPath), ".netsh-dns.json-*")
	if err != nil {
		return fmt.Errorf("creating netsh dns marker: %w", err)
	}
	defer func() { _ = os.Remove(temp.Name()) }()

	if _, err := temp.Write(raw); err != nil {
		_ = temp.Close()

		return fmt.Errorf("writing netsh dns marker: %w", err)
	}

	if err := temp.Sync(); err != nil {
		_ = temp.Close()

		return fmt.Errorf("syncing netsh dns marker: %w", err)
	}

	if err := temp.Close(); err != nil {
		return fmt.Errorf("closing netsh dns marker: %w", err)
	}

	if err := os.Rename(temp.Name(), b.markerPath); err != nil {
		return fmt.Errorf("renaming netsh dns marker: %w", err)
	}

	return nil
}

type netshMarker struct {
	Iface string `json:"iface"`
}

type windowsDNSBackend struct {
	dnsBackend

	netsh netshBackend
}

func newWindowsDNSBackend(kind, dir string, run runFunc) dnsBackend {
	backend := netshBackend{
		markerPath: filepath.Join(dir, netshMarkerFileName),
		run:        run,
	}
	if kind == config.DNSNetsh {
		return backend
	}

	return windowsDNSBackend{
		dnsBackend: noopBackend{},
		netsh:      backend,
	}
}

func (b windowsDNSBackend) Cleanup(ctx context.Context) error { return b.netsh.Cleanup(ctx) }

func splitFamilies(servers []string) ([]string, []string, error) {
	var v4, v6 []string

	for _, server := range servers {
		addr, err := netip.ParseAddr(server)
		if err != nil {
			return nil, nil, fmt.Errorf("parsing dns server %q: %w", server, err)
		}

		addr = addr.Unmap()
		if addr.Is4() {
			v4 = append(v4, addr.String())
		} else {
			v6 = append(v6, addr.String())
		}
	}

	return v4, v6, nil
}
