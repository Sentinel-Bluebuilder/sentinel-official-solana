package client_test

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	if os.Getenv("SENTINEL_PF_COMMAND") == "1" {
		os.Exit(fakePFCommand())
	}

	os.Exit(m.Run())
}
