package sample

// Sample describes the values we will return with each execution.
type Sample struct {
	Latency        float64
	Latency50ptile float64
	Latency99ptile float64
	Throughput     float64
	LossPercent    float64
	Retransmits    float64
	TransportMSS   int // Smallest TCP MSS of the client's streams; 0 when not reported
	Metric         string
	Driver         string
}
