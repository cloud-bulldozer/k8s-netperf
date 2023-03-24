package sample

// Sample describes the values we will return with each execution.
type Sample struct {
	Latency        float64
	Latency99ptile float64
	Throughput     float64
	Metric         string
	Driver         string
}
