package client

type WFPFirewall = wfpFirewall

func NewFirewall(dir, name string) firewallBackend { return newFirewall(dir, name) }
