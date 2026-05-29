package metrics

import (
	"encoding/json"
	"io"
	"math"
	"runtime"
	"time"
)

type Sampler struct {
	TimeSeriesSamples []TimeSeriesSample
	StartTime         time.Time

	crawlMetrics *Counter
	ticker       *time.Ticker

	stop chan struct{}
	done chan struct{}
}

type TimeSeriesSample struct {
	ElapsedS int64
	Queued   int64
	Claimed  int64
	Crawled  int64
	Success  int64
	HTTP429  int64

	ActiveWorkers int64
	Goroutines    int
	HeapMB        float64
}

func NewSampler(crawlMetrics *Counter) *Sampler {
	return &Sampler{
		TimeSeriesSamples: []TimeSeriesSample{},
		StartTime:         time.Now(),
		crawlMetrics:      crawlMetrics,
		ticker:            time.NewTicker(5 * time.Second),
		stop:              make(chan struct{}),
		done:              make(chan struct{}),
	}
}

func (s *Sampler) sample(currentTime time.Time) {
	var sam TimeSeriesSample
	sam.ElapsedS = int64(currentTime.Sub(s.StartTime).Seconds())

	sam.Queued, sam.Claimed, sam.Crawled, sam.Success, sam.HTTP429, sam.ActiveWorkers = s.crawlMetrics.Snapshot()

	sam.Goroutines = runtime.NumGoroutine()

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	sam.HeapMB = float64(m.HeapAlloc) / 1024 / 1024
	sam.HeapMB = math.Trunc(sam.HeapMB*100) / 100
	s.TimeSeriesSamples = append(s.TimeSeriesSamples, sam)

}

func (s *Sampler) Run() {
	defer close(s.done)
	defer s.ticker.Stop()
	for {
		select {
		case <-s.stop:
			s.sample(time.Now())
			return
		case t := <-s.ticker.C:
			s.sample(t)
		}
	}
}

func (s *Sampler) WaitAndClose() {
	close(s.stop)
	<-s.done
}

func (s *Sampler) EncodeTo(f io.Writer) error {
	encoder := json.NewEncoder(f)
	err := encoder.Encode(s)
	return err
}
