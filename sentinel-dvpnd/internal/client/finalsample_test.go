package client_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-dvpnd/internal/client"
	"github.com/sentinel-official/sentinel-go-sdk/v3/sdktest"
	"github.com/sentinel-official/sentinel-go-sdk/v3/service"
)

// A protocol that runs as a child process lives only as long as the context it was started with. A stop must
// not end that context before the final usage sample, or the usage since the last periodic sample is lost.
func TestRuntime_StopSession_FinalSampleSeesTheServiceStillRunning(t *testing.T) { //nolint:paralleltest // timing
	rt, home, state := newIssuerFixture(t, 100_000, false)

	var started atomic.Pointer[context.Context]
	onStart := func(ctx context.Context) { started.Store(&ctx) }
	gone := func() error {
		if p := started.Load(); p != nil && (*p).Err() != nil {
			return errors.New("process exited") // what a child process started with that context reports
		}

		return nil
	}
	client.SetService(rt, sdktest.FakeName, fakeClientConfig{
		hooks: clientHooks{onStart: &onStart, onStatsError: &gone}, state: state,
	})

	row := startIssuer(t, rt, home)
	time.Sleep(100 * time.Millisecond)

	state.SetLocalStats(service.PeerStats{RxBytes: 5_000_000}) // moved after the last periodic sample
	require.NoError(t, client.StopSession(t.Context(), rt))

	var stored client.Session
	require.NoError(t, home.DB.First(&stored, "id = ?", row.ID).Error)
	require.EqualValues(t, 5_000_000, stored.RxBytes, "the final sample counts the usage up to the stop")
}
