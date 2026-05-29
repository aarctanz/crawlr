package metrics

import (
	"fmt"
	"math"
	"strings"
)

type PageLatency struct {
	FetchMs    int
	ParseMs    int
	PageSizeKB int

	TotalMs int
}

type LatencyMetrics struct {
	latencyChannel chan PageLatency

	fetchLatencyCount []int
	pageSizeKBCount   []int
	parseLatencyCount []int
	totalLatencyCount []int

	done chan struct{}
}

var (
	latencyBuckets = []int{1, 2, 5, 10, 15, 25, 50, 100, 250, 500, 1000, 2500, 5000, 10000, 15000, 20000, 30000, 60000, math.MaxInt}
	sizeBuckets    = []int{25, 50, 75, 100, 250, 500, 750, 1000, 1500, 2000, 2500, 5000, math.MaxInt}
)

func NewLatencyMetrics(maxPages int) *LatencyMetrics {
	return &LatencyMetrics{
		latencyChannel:    make(chan PageLatency, maxPages),
		fetchLatencyCount: make([]int, len(latencyBuckets)),
		pageSizeKBCount:   make([]int, len(sizeBuckets)),
		parseLatencyCount: make([]int, len(latencyBuckets)),
		totalLatencyCount: make([]int, len(latencyBuckets)),
		done:              make(chan struct{}),
	}
}

func (lm *LatencyMetrics) WaitAndClose() {
	close(lm.latencyChannel)
	<-lm.done
}

func (lm *LatencyMetrics) Record(latency PageLatency) {
	lm.latencyChannel <- latency
}

func (lm *LatencyMetrics) Run() {
	for latency := range lm.latencyChannel {
		incCounts(latency.FetchMs, lm.fetchLatencyCount, latencyBuckets)
		incCounts(latency.ParseMs, lm.parseLatencyCount, latencyBuckets)
		incCounts(latency.PageSizeKB, lm.pageSizeKBCount, sizeBuckets)
		incCounts(latency.TotalMs, lm.totalLatencyCount, latencyBuckets)
	}
	close(lm.done)
}

func (lm *LatencyMetrics) FetchPercentile(p float64) int {
	return percentile(p, lm.fetchLatencyCount, latencyBuckets)
}

func (lm *LatencyMetrics) ParsePercentile(p float64) int {
	return percentile(p, lm.parseLatencyCount, latencyBuckets)
}

func (lm *LatencyMetrics) PageSizePercentile(p float64) int {
	return percentile(p, lm.pageSizeKBCount, sizeBuckets)
}

func (lm *LatencyMetrics) TotalPercentile(p float64) int {
	return percentile(p, lm.totalLatencyCount, latencyBuckets)
}

func (lm *LatencyMetrics) Report() string {
	var b strings.Builder
	renderHistogram(&b, "Fetch latency", "ms", lm.fetchLatencyCount, latencyBuckets)
	renderHistogram(&b, "Parse latency", "ms", lm.parseLatencyCount, latencyBuckets)
	renderHistogram(&b, "Total latency", "ms", lm.totalLatencyCount, latencyBuckets)
	renderHistogram(&b, "Page size", "KB", lm.pageSizeKBCount, sizeBuckets)
	return b.String()
}

func renderHistogram(b *strings.Builder, title, unit string, counts, buckets []int) {
	total, maxCount := 0, 0
	for _, c := range counts {
		total += c
		if c > maxCount {
			maxCount = c
		}
	}

	fmt.Fprintf(b, "%s (n=%d)\n", title, total)
	if total == 0 {
		b.WriteString("  (no samples)\n\n")
		return
	}

	const barWidth = 40
	for i, c := range counts {
		bars := c * barWidth / maxCount
		pct := float64(c) / float64(total) * 100
		fmt.Fprintf(b, "  %14s | %s%s %7d  %5.1f%%\n",
			bucketLabel(i, buckets, unit),
			strings.Repeat("█", bars),
			strings.Repeat(" ", barWidth-bars),
			c, pct)
	}
	fmt.Fprintf(b, "  p50=%d%s  p95=%d%s  p99=%d%s\n\n",
		percentile(0.50, counts, buckets), unit,
		percentile(0.95, counts, buckets), unit,
		percentile(0.99, counts, buckets), unit)
}

func bucketLabel(i int, buckets []int, unit string) string {
	upper := buckets[i]
	lower := 0
	if i > 0 {
		lower = buckets[i-1]
	}
	if upper == math.MaxInt {
		return fmt.Sprintf("> %d%s", lower, unit)
	}
	return fmt.Sprintf("%d-%d%s", lower, upper, unit)
}

func percentile(p float64, counts []int, buckets []int) int {
	total := 0
	for _, i := range counts {
		total += i
	}

	rank := int(math.Ceil(p * float64(total)))

	cumulative := 0
	for i, val := range counts {
		cumulative += val
		if cumulative >= rank {
			lower := 0
			if i > 0 {
				lower = buckets[i-1]
			}
			if i == len(counts)-1 {
				return buckets[len(buckets)-2]
			}
			upper := buckets[i]
			fraction := float64(rank-(cumulative-val)) / float64(val)
			return lower + int(fraction*float64(upper-lower))
		}
	}
	return buckets[len(buckets)-2]
}

func incCounts(value int, Counts []int, Buckets []int) {
	for i, bucket := range Buckets {
		if value <= bucket {
			Counts[i]++
			return
		}
	}
}
