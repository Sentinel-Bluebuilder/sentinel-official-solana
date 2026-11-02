package apiserver_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-dvpnd/internal/apiserver"
)

const requestsMetric = "sentinel_dvpnd_api_requests_total"

func TestServer_CountAPIRequests_LabelsUnknownRoutes(t *testing.T) {
	t.Parallel()

	reg := prometheus.NewRegistry()

	server, err := apiserver.New(t.TempDir(), 0, zerolog.Nop(), reg, nil)
	require.NoError(t, err)

	resp := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/nope", nil)
	server.Engine().ServeHTTP(resp, req)
	require.Equal(t, http.StatusNotFound, resp.Code)

	families, err := reg.Gather()
	require.NoError(t, err)

	counts := map[string]float64{}

	for _, family := range families {
		if family.GetName() != requestsMetric {
			continue
		}

		for _, metric := range family.GetMetric() {
			labels := map[string]string{}
			for _, label := range metric.GetLabel() {
				labels[label.GetName()] = label.GetValue()
			}

			counts[labels["route"]+"/"+labels["result"]] = metric.GetCounter().GetValue()
		}
	}

	require.Equal(t, map[string]float64{"unknown/error": 1}, counts)
}
