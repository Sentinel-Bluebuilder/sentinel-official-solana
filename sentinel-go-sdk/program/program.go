package program

import (
	"encoding/hex"
	"errors"
	"fmt"
	"math/bits"
	"strings"

	"github.com/sentinel-official/sentinel-programs/clients/go/dvpn"
)

const (
	PermIssueTickets         = dvpn.PermOpenLease
	PermSignNodeMetadata     = dvpn.PermUpdateNode
	PermSignProviderMetadata = dvpn.PermUpdateProvider
	PermUseChannel           = dvpn.PermOpenChannel
	PermUseLease             = dvpn.PermOpenLease
	PermUseSubscription      = dvpn.PermOpenSubscription
)

const (
	PermsClient = dvpn.PermOpenChannel | dvpn.PermFundChannels | dvpn.PermStartCloseChannel |
		dvpn.PermCloseChannel | dvpn.PermOpenSubscription
	PermsNode     = dvpn.PermListNode | dvpn.PermUpdateNode
	PermsProvider = dvpn.PermUpdateProvider | dvpn.PermOpenLease | dvpn.PermFundLeases |
		dvpn.PermStartCloseLease | dvpn.PermCloseLease
)

const SecondsPerHour = 3_600

var (
	ErrInvalidCountry = errors.New("country must be two ASCII letters")
	ErrInvalidID      = errors.New("id must be 64 hexadecimal characters")
)

func Live(node dvpn.Node, cfg dvpn.Config, now int64) bool {
	return node.ListedAt > 0 && now < node.ListedAt+int64(cfg.NodeListingTTLSeconds)
}

func NodeURI(node dvpn.Node) string {
	return formatURI(node.URI[:], node.URILen)
}

func ParseCountry(text string) ([2]uint8, error) {
	upper := strings.ToUpper(text)
	if len(upper) != 2 || upper[0] < 'A' || upper[0] > 'Z' || upper[1] < 'A' || upper[1] > 'Z' {
		return [2]uint8{}, fmt.Errorf("%w: %s", ErrInvalidCountry, text)
	}

	return [2]uint8{upper[0], upper[1]}, nil
}

func ParseID(text string) ([32]uint8, error) {
	var id [32]uint8

	raw, err := hex.DecodeString(text)
	if err != nil || len(raw) != len(id) {
		return id, fmt.Errorf("%w: %q", ErrInvalidID, text)
	}

	copy(id[:], raw)

	return id, nil
}

func ProviderURI(provider dvpn.Provider) string {
	return formatURI(provider.URI[:], provider.URILen)
}

func StreamAccrued(escrow, streamBase, ratePerHour uint64, startedAt, now int64) uint64 {
	var elapsed uint64
	if now > startedAt {
		elapsed = uint64(now - startedAt)
	}

	high, low := bits.Mul64(ratePerHour, elapsed)
	if high >= SecondsPerHour {
		return escrow
	}

	product, _ := bits.Div64(high, low, SecondsPerHour)

	raw, carry := bits.Add64(streamBase, product, 0)
	if carry != 0 {
		return escrow
	}

	return min(raw, escrow)
}

func formatURI(raw []byte, length uint8) string {
	if int(length) > len(raw) {
		return ""
	}

	return string(raw[:length])
}
