package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math/big"
	"net"
	"net/netip"
)

const (
	maxPort = 65535
	minPort = 1024
)

func CheckPortFree(ctx context.Context, port uint16) error {
	var listenConfig net.ListenConfig

	addr := fmt.Sprintf("127.0.0.1:%d", port)

	listener, err := listenConfig.Listen(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("checking %s: %w", addr, err)
	}

	if err := listener.Close(); err != nil {
		return fmt.Errorf("closing %s: %w", addr, err)
	}

	return nil
}

func FreePort() (uint16, error) {
	var listenConfig net.ListenConfig

	listener, err := listenConfig.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("listening for a free port: %w", err)
	}

	defer func() { _ = listener.Close() }()

	addr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		return 0, fmt.Errorf("listening for a free port: unexpected address %s", listener.Addr())
	}

	return uint16(addr.Port), nil
}

func RandomHex(byteLen int) string {
	buf := make([]byte, byteLen)
	if _, err := rand.Read(buf); err != nil {
		panic(err)
	}

	return hex.EncodeToString(buf)
}

func RandomPort() uint16 {
	return uint16(RandomUint32(minPort, maxPort))
}

func RandomPrefix() netip.Prefix {
	return netip.MustParsePrefix(fmt.Sprintf("10.%d.%d.1/24", RandomUint32(0, 255), RandomUint32(0, 255)))
}

func RandomUint32(low, high uint32) uint32 {
	offset, err := rand.Int(rand.Reader, big.NewInt(int64(high-low+1)))
	if err != nil {
		panic(err)
	}

	return low + uint32(offset.Int64())
}
