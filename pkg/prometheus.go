package pkg

import "github.com/prometheus/client_golang/prometheus"

// 定义 Prometheus 指标
var (
	requestCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "vegeta_requests_total",
			Help: "Total number of HTTP requests made by Vegeta.",
		},
		[]string{"status", "endpoint"},
	)

	latencyHistogram = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "vegeta_request_latency_seconds",
			Help:    "Histogram of latencies for requests.",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"endpoint"},
	)

	qpsGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "vegeta_qps",
			Help: "Requests per second (QPS) for each endpoint.",
		},
		[]string{"endpoint"},
	)
)

func init() {
	// 注册 Prometheus 指标
	prometheus.MustRegister(requestCount)
	prometheus.MustRegister(latencyHistogram)
	prometheus.MustRegister(qpsGauge)
}
