package api

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"

	"github.com/sentinel-official/sentinel-go-sdk/v3/service"
)

const (
	defaultPeerPort uint16 = 443
	maxPeerURLBytes int    = 128
)

var ErrInvalidPeerURL = errors.New("invalid peer url")

type PeerURL struct {
	Host string
	Port uint16
}

func (u PeerURL) Addr() string {
	return net.JoinHostPort(u.Host, strconv.FormatUint(uint64(u.Port), 10))
}

func (u PeerURL) String() string {
	return "https://" + u.Addr()
}

func ParsePeerURL(raw string) (PeerURL, error) {
	if len(raw) > maxPeerURLBytes {
		return PeerURL{}, fmt.Errorf("%w: length: %d bytes", ErrInvalidPeerURL, len(raw))
	}

	u, err := url.Parse(raw)
	if err != nil {
		return PeerURL{}, fmt.Errorf("%w: syntax: %q", ErrInvalidPeerURL, raw)
	}

	if u.Scheme != "https" {
		return PeerURL{}, fmt.Errorf("%w: scheme: %q", ErrInvalidPeerURL, raw)
	}

	if u.User != nil {
		return PeerURL{}, fmt.Errorf("%w: user info: %q", ErrInvalidPeerURL, raw)
	}

	if u.RawQuery != "" || u.ForceQuery {
		return PeerURL{}, fmt.Errorf("%w: query: %q", ErrInvalidPeerURL, raw)
	}

	if u.Fragment != "" {
		return PeerURL{}, fmt.Errorf("%w: fragment: %q", ErrInvalidPeerURL, raw)
	}

	if u.Path != "" && u.Path != "/" {
		return PeerURL{}, fmt.Errorf("%w: path: %q", ErrInvalidPeerURL, raw)
	}

	host := strings.ToLower(u.Hostname())
	if !isPeerHost(host) {
		return PeerURL{}, fmt.Errorf("%w: host: %q", ErrInvalidPeerURL, raw)
	}

	port := defaultPeerPort

	if strings.HasSuffix(u.Host, ":") {
		return PeerURL{}, fmt.Errorf("%w: port: %q", ErrInvalidPeerURL, raw)
	}

	if p := u.Port(); p != "" {
		v, err := strconv.ParseUint(p, 10, 16)
		if err != nil || v == 0 {
			return PeerURL{}, fmt.Errorf("%w: port: %q", ErrInvalidPeerURL, raw)
		}

		port = uint16(v)
	}

	return PeerURL{
		Host: host,
		Port: port,
	}, nil
}

func isPeerHost(host string) bool {
	if addr, err := netip.ParseAddr(host); err == nil {
		return addr.Zone() == ""
	}

	return service.ValidateHostname(host) == nil
}
