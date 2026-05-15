package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aarctanz/crawlr/parser"
)

type CrawlerStats struct {
	Sample    []TimeSeriesSample
	StartTime time.Time
}

type TimeSeriesSample struct {
	ElapsedS     int64
	PagesTotal   int
	ClaimedTotal int
	Success      int
	HeapMB       float64
	Goroutines   int
}

func (c *CrawlerStats) update(crawled map[string]struct{}, mu *sync.Mutex, currentTime time.Time, success *atomic.Int32, claimed map[string]struct{}) {
	var s TimeSeriesSample
	s.ElapsedS = int64(currentTime.Sub(c.StartTime).Seconds())

	mu.Lock()
	s.PagesTotal = len(crawled)
	s.ClaimedTotal = len(claimed)
	mu.Unlock()

	s.Success = int(success.Load())
	s.Goroutines = runtime.NumGoroutine()

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	s.HeapMB = float64(m.HeapAlloc) / 1024 / 1024
	s.HeapMB = math.Trunc(s.HeapMB*100) / 100
	c.Sample = append(c.Sample, s)
}

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintf(os.Stderr, "usage: crawlr <seed-url> <max-pages>\n")
		os.Exit(1)
	}

	seed := os.Args[1]
	maxPages, err := strconv.Atoi(os.Args[2])
	if err != nil {
		fmt.Fprintf(os.Stderr, "max-pages must be an integer\n")
		os.Exit(1)
	}

	var success atomic.Int32

	claimed := make(map[string]struct{})
	crawled := make(map[string]struct{})
	var mu sync.Mutex
	var wg sync.WaitGroup

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	done := make(chan bool)

	start := time.Now()
	crawlerStats := CrawlerStats{StartTime: start}
	go func(t *time.Ticker) {
		for {
			select {
			case <-done:
				crawlerStats.update(crawled, &mu, time.Now(), &success, claimed)
				return

			case t := <-t.C:
				crawlerStats.update(crawled, &mu, t, &success, claimed)
			}
		}
	}(ticker)

	fetchLatencyChannel := make(chan int, maxPages)
	parseLatencyChannel := make(chan int, maxPages)
	pageSizeChannel := make(chan int, maxPages)
	var lwg sync.WaitGroup

	go func() {
		<-done
		close(fetchLatencyChannel)
		close(parseLatencyChannel)
		close(pageSizeChannel)
	}()

	latencyBuckets := []int{1, 2, 5, 10, 15, 25, 50, 100, 250, 500, 1000, 2500, 5000, 10000, 15000, 20000, 30000, 60000, math.MaxInt}
	sizeBuckets := []int{25, 50, 75, 100, 250, 500, 750, 1000, 1500, 2000, 2500, 5000, math.MaxInt}

	fetchLatencyCounts := make([]int, len(latencyBuckets))
	parseLatencyCounts := make([]int, len(latencyBuckets))
	pageSizeCounts := make([]int, len(sizeBuckets))

	lwg.Add(3)
	go func() {
		defer lwg.Done()
		for val := range fetchLatencyChannel {
			incCounts(val, fetchLatencyCounts, latencyBuckets)
		}
	}()

	go func() {
		defer lwg.Done()
		for val := range parseLatencyChannel {
			incCounts(val, parseLatencyCounts, latencyBuckets)
		}
	}()

	go func() {
		defer lwg.Done()
		for val := range pageSizeChannel {
			incCounts(val, pageSizeCounts, sizeBuckets)
		}
	}()

	wg.Add(1)
	go fetch(seed, crawled, claimed, &mu, &wg, maxPages, &success, fetchLatencyChannel, parseLatencyChannel, pageSizeChannel)
	wg.Wait()
	close(done)
	lwg.Wait()
	totalTime := time.Since(start)
	fmt.Printf("crawled %d pages, total time: %.2fs\n", len(crawled), totalTime.Seconds())
	fmt.Printf("Success: %d\n", success.Load())

	file, err := os.OpenFile("stats.json", os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		fmt.Println(err)
		return
	}
	defer file.Close()
	encoder := json.NewEncoder(file)
	encoder.Encode(crawlerStats)
	fmt.Printf("%+v\n", fetchLatencyCounts)
	fmt.Printf("%+v\n", parseLatencyCounts)
	fmt.Printf("%+v\n", pageSizeCounts)

	fetchP95 := percentile(0.95, fetchLatencyCounts, latencyBuckets)
	fmt.Printf("fetch P95: %dms\n", fetchP95)
	fetchP99 := percentile(0.99, fetchLatencyCounts, latencyBuckets)
	fmt.Printf("fetch P99: %dms\n", fetchP99)
	pageSizeP95 := percentile(0.95, pageSizeCounts, sizeBuckets)
	fmt.Printf("page size P95: %dKB\n", pageSizeP95)
	pageSizeP99 := percentile(0.99, pageSizeCounts, sizeBuckets)
	fmt.Printf("page size P99: %dKB\n", pageSizeP99)
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

func fetch(rawUrl string, crawled map[string]struct{}, claimed map[string]struct{}, mu *sync.Mutex, wg *sync.WaitGroup, maxPages int, success *atomic.Int32, fetchLatencyChannel chan int, parseLatencyChannel chan int, pageSizeChannel chan int) {
	defer wg.Done()

	mu.Lock()
	if len(claimed) >= maxPages {
		mu.Unlock()
		return
	}

	if _, ok := claimed[rawUrl]; ok {
		mu.Unlock()
		return
	}
	claimed[rawUrl] = struct{}{}
	mu.Unlock()

	start := time.Now()
	resp, err := http.Get(rawUrl)
	fetchTime := time.Since(start)

	if err != nil {
		totalTime := time.Since(start)
		totalTimeString := fmt.Sprintf("total %dms | fetch %dms", totalTime.Milliseconds(), fetchTime.Milliseconds())
		fmt.Printf("ERR %s: %v (%s)\n", rawUrl, err, totalTimeString)
		mu.Lock()
		crawled[rawUrl] = struct{}{}
		mu.Unlock()
		return
	}

	if resp.StatusCode != http.StatusOK {
		totalTime := time.Since(start)
		totalTimeString := fmt.Sprintf("total %dms | fetch %dms", totalTime.Milliseconds(), fetchTime.Milliseconds())
		fmt.Printf("ERR %s: HTTP error: %d (%s)\n", rawUrl, resp.StatusCode, totalTimeString)
		mu.Lock()
		crawled[rawUrl] = struct{}{}
		mu.Unlock()
		return
	}

	base, err := url.Parse(resp.Request.URL.String())
	if err != nil {
		totalTime := time.Since(start)
		totalTimeString := fmt.Sprintf("total %dms | fetch %dms", totalTime.Milliseconds(), fetchTime.Milliseconds())
		fmt.Printf("ERR %s: %v (%s)\n", rawUrl, err, totalTimeString)
		mu.Lock()
		crawled[rawUrl] = struct{}{}
		mu.Unlock()
		return
	}
	readStart := time.Now()
	body, err := io.ReadAll(resp.Body)
	readTime := time.Since(readStart)
	resp.Body.Close()

	if err != nil {
		totalTime := time.Since(start)
		totalTimeString := fmt.Sprintf("total %dms | fetch %dms | read %dms", totalTime.Milliseconds(), fetchTime.Milliseconds(), readTime.Milliseconds())
		fmt.Printf("ERR %s: %v (%s)\n", rawUrl, err, totalTimeString)
		mu.Lock()
		crawled[rawUrl] = struct{}{}
		mu.Unlock()
		return
	}

	pageSizeKB := int(len(body) / 1024)
	resp.Body = io.NopCloser(bytes.NewReader(body))
	links, parseTime := parser.Parse(resp, base)
	totalTime := time.Since(start)
	timeSpent := fmt.Sprintf("total %dms | fetch %dms | read %dms | parse %dms | size %dKB", totalTime.Milliseconds(), fetchTime.Milliseconds(), readTime.Milliseconds(), parseTime.Milliseconds(), pageSizeKB)
	success.Add(1)
	fmt.Printf("OK %s (%s)\n", rawUrl, timeSpent)

	fetchLatencyChannel <- int(fetchTime.Milliseconds())
	parseLatencyChannel <- int(parseTime.Milliseconds())
	pageSizeChannel <- pageSizeKB

	mu.Lock()
	crawled[rawUrl] = struct{}{}
	mu.Unlock()

	for _, link := range links {
		wg.Add(1)
		go fetch(link, crawled, claimed, mu, wg, maxPages, success, fetchLatencyChannel, parseLatencyChannel, pageSizeChannel)
	}
}
