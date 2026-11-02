package api

import (
	"errors"
	"fmt"
	"net/netip"
)

var ErrAddrNotPublic = errors.New("address is not public")

//nolint:gochecknoglobals
var reservedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("2001:db8::/32"),
}

func ValidatePublicAddr(addr netip.Addr) error {
	if !isPublic(addr.Unmap()) {
		return fmt.Errorf("%w: %s", ErrAddrNotPublic, addr)
	}

	return nil
}

func isPublic(addr netip.Addr) bool {
	if !addr.IsGlobalUnicast() || addr.IsPrivate() {
		return false
	}

	for _, prefix := range reservedPrefixes {
		if prefix.Contains(addr) {
			return false
		}
	}

	return true
}
