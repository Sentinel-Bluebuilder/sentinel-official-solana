package config

import (
	"github.com/sentinel-official/sentinel-go-sdk/v3/service"
)

func DefaultDNSBackendFor(goos string) string { return defaultDNSBackend(goos) }

func DNSBackendsFor(goos string) []string { return dnsBackends(goos) }

func LoadedDerivePlacement(l *Loaded, name service.Name, goos string) service.Placement {
	return l.derivePlacement(name, goos)
}

func LoadedEndpoint(l *Loaded, goos string) string { return l.endpoint(goos) }

func RoleSupportedOn(role, goos string) bool { return roleSupported(role, goos) }
