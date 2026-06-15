package main

import (
	"github.com/gin-gonic/gin"
	"github.com/hibiken/asynq"
	asynqmetrics "github.com/hibiken/asynq/x/metrics"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// initMetrics registers the asynq queue collector (queue depth, processed,
// failed, retries, latency — all read from Redis, so the API can expose them
// regardless of which process does the work).
func initMetrics() error {
	opt, err := redisConnOpt()
	if err != nil {
		return err
	}
	insp := asynq.NewInspector(opt)
	prometheus.MustRegister(asynqmetrics.NewQueueMetricsCollector(insp))
	return nil
}

// metricsHandler serves the Prometheus exposition format at /metrics.
func metricsHandler() gin.HandlerFunc {
	h := promhttp.Handler()
	return func(c *gin.Context) { h.ServeHTTP(c.Writer, c.Request) }
}
