//go:build linux

package provider

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

const (
	kindLease      = "lease"
	resultAccepted = "accepted"
	resultIssued   = "issued"
)

type metrics struct {
	leasesOpen prometheus.Gauge
	tickets    *prometheus.CounterVec
	vouchers   *prometheus.CounterVec
}

func newMetrics(reg prometheus.Registerer) metrics {
	factory := promauto.With(reg)
	ticketsLabelNames := []string{"result"}
	vouchersLabelNames := []string{"kind", "result"}

	return metrics{
		leasesOpen: factory.NewGauge(prometheus.GaugeOpts{
			Help: "Number of currently open leases.",
			Name: "sentinel_dvpnd_leases_open",
		}),
		tickets: factory.NewCounterVec(prometheus.CounterOpts{
			Help: "Total number of access tickets issued, by result.",
			Name: "sentinel_dvpnd_tickets_total",
		}, ticketsLabelNames),
		vouchers: factory.NewCounterVec(prometheus.CounterOpts{
			Help: "Total number of vouchers, by kind and result.",
			Name: "sentinel_dvpnd_vouchers_total",
		}, vouchersLabelNames),
	}
}
