package hysteria2

import (
	"net/netip"
)

const Binary = binary

// WriteConf renders with no local ranges, so the golden files do not depend on the host's routes.
func WriteConf(path string, addr netip.Addr, iface string, resp PeerResponse) error {
	return writeConfWith(path, addr, iface, resp, nil, nil)
}
