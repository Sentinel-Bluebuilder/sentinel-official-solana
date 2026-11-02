//go:build linux

package node

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

type metrics struct {
	redemptions         *prometheus.CounterVec
	sessionBytes        *prometheus.CounterVec
	sessionsActive      prometheus.Gauge
	unredeemedBaseUnits prometheus.Gauge
	voucherRequests     *prometheus.CounterVec
	vouchers            *prometheus.CounterVec
}

func newMetrics(reg prometheus.Registerer) metrics {
	factory := promauto.With(reg)

	return metrics{
		redemptions: factory.NewCounterVec(prometheus.CounterOpts{
			Help: "Total number of on-chain redemption attempts.",
			Name: "sentinel_dvpnd_redemptions_total",
		}, []string{"kind", "mode", "result"}),
		sessionBytes: factory.NewCounterVec(prometheus.CounterOpts{
			Help: "Total number of bytes transferred over active sessions.",
			Name: "sentinel_dvpnd_session_bytes_total",
		}, []string{"direction"}),
		sessionsActive: factory.NewGauge(prometheus.GaugeOpts{
			Help: "Number of currently active sessions.",
			Name: "sentinel_dvpnd_sessions_active",
		}),
		unredeemedBaseUnits: factory.NewGauge(prometheus.GaugeOpts{
			Help: "Number of unredeemed base units across open positions.",
			Name: "sentinel_dvpnd_unredeemed_base_units",
		}),
		voucherRequests: factory.NewCounterVec(prometheus.CounterOpts{
			Help: "Total number of voucher requests published.",
			Name: "sentinel_dvpnd_voucher_requests_total",
		}, []string{"kind"}),
		vouchers: factory.NewCounterVec(prometheus.CounterOpts{
			Help: "Total number of vouchers, by kind and result.",
			Name: "sentinel_dvpnd_vouchers_total",
		}, []string{"kind", "result"}),
	}
}
