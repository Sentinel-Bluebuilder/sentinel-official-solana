//go:build e2e && integration

package e2e_test

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-go-sdk/v3/nodeapi"
)

const (
	leaseHealthPrepaySeconds         = 60
	leaseHealthPricePerHourBaseUnits = 3_600
	leaseHealthTimeout               = 150 * time.Second
)

//nolint:paralleltest
func TestLeaseHealth_Localnet(t *testing.T) {
	for _, svc := range servicesToTest() {
		t.Run(svc, func(t *testing.T) { runLeaseHealth(t, svc) })
	}
}

func runLeaseHealth(t *testing.T, svc string) {
	t.Helper()

	lf := &localnetFixture{
		nodeLeasePricePerHourBaseUnits: leaseHealthPricePerHourBaseUnits,
		service:                        svc,
	}
	ctx := t.Context()
	lf.startInfra(t, ctx)
	lf.initRoles(t, ctx)

	setInstanceConfig(t, lf.nodeHome, leaseHealthPrepaySeconds, "lease", "min_prepay_seconds")
	setInstanceConfig(t, lf.providerHome, leaseHealthPrepaySeconds, "lease", "prepay_seconds")
	setInstanceConfig(t, lf.nodeHome, leaseHealthPricePerHourBaseUnits, "prices", "lease_per_hour")
	lf.registerOnChain(t, ctx)
	lf.startDaemons(t, ctx)

	clientCLI := lf.cli.withKey("client-owner").withDaemon(lf.clientTag, lf.clientHome)
	session := lf.startTicketSession(t, ctx)
	waitForCondition(t, checkSessionActive(ctx, clientCLI, session.GetId()))

	stopDaemon(t, lf.providerProc, "provider")
	waitForConditionWithin(t, checkSessionEnded(ctx, clientCLI, session.GetId()), leaseHealthTimeout)

	ended := sessionStatus(t, clientCLI, session.GetId())
	reason := strings.ToLower(ended.GetError())
	require.True(t, strings.Contains(reason, nodeapi.ReasonCreditExhausted) || strings.Contains(reason, "lease"),
		"ticket session must end because its lease is unhealthy: %s", ended.GetError())
}
