package client

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

const (
	directionRx    = "rx"
	directionTx    = "tx"
	kindChannel    = "channel"
	resultAccepted = "accepted"
	resultRejected = "rejected"
)

type metrics struct {
	sessionBytes   *prometheus.CounterVec
	sessionsActive prometheus.Gauge
	vouchers       *prometheus.CounterVec
}

func newMetrics(reg prometheus.Registerer) metrics {
	factory := promauto.With(reg)

	sessionBytesOpts := prometheus.CounterOpts{
		Help: "Total number of bytes transferred over active sessions.",
		Name: "sentinel_dvpnd_session_bytes_total",
	}
	sessionsOpts := prometheus.GaugeOpts{
		Help: "Number of currently active sessions.",
		Name: "sentinel_dvpnd_sessions_active",
	}
	vouchersOpts := prometheus.CounterOpts{
		Help: "Total number of vouchers, by kind and result.",
		Name: "sentinel_dvpnd_vouchers_total",
	}

	sessionBytesLabels := []string{"direction"}
	vouchersLabels := []string{"kind", "result"}

	return metrics{
		sessionBytes:   factory.NewCounterVec(sessionBytesOpts, sessionBytesLabels),
		sessionsActive: factory.NewGauge(sessionsOpts),
		vouchers:       factory.NewCounterVec(vouchersOpts, vouchersLabels),
	}
}
