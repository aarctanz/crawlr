package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
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

func (c *CrawlerStats) update(crawler *Crawler, currentTime time.Time) {
	var s TimeSeriesSample
	s.ElapsedS = int64(currentTime.Sub(c.StartTime).Seconds())

	crawler.mu.Lock()
	s.PagesTotal = len(crawler.crawled)
	s.ClaimedTotal = len(crawler.claimed)
	crawler.mu.Unlock()

	s.Success = int(crawler.SuccessPages.Load())
	s.Goroutines = runtime.NumGoroutine()

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	s.HeapMB = float64(m.HeapAlloc) / 1024 / 1024
	s.HeapMB = math.Trunc(s.HeapMB*100) / 100
	c.Sample = append(c.Sample, s)
}

var httpClient = &http.Client{
	Timeout: 20 * time.Second,
}

type Queue struct {
	queue []string
	head  int
}

func NewQueue() *Queue {
	return &Queue{
		head:  0,
		queue: make([]string, 0, 4096),
	}
}

func (q *Queue) Enqueue(urls []string) {
	q.queue = append(q.queue, urls...)
}

func (q *Queue) Dequeue() (string, bool) {
	if q.head == len(q.queue) {
		return "", false
	}

	url := q.queue[q.head]
	q.queue[q.head] = ""
	q.head += 1
	if q.head > len(q.queue)/2 {
		n := copy(q.queue, q.queue[q.head:])
		q.queue = q.queue[:n]
		q.head = 0
	}
	return url, true
}

func (q *Queue) Len() int {
	return len(q.queue) - q.head
}

type Crawler struct {
	crawled       map[string]struct{}
	claimed       map[string]struct{}
	mu            *sync.Mutex
	cond          *sync.Cond
	wg            *sync.WaitGroup
	MaxPages      int
	TotalPages    atomic.Int32
	SuccessPages  atomic.Int32
	ActiveWorkers int
	IsShutdown    bool
	Que           *Queue
}

func NewCrawler(seed string, maxPages int) *Crawler {
	crawled := make(map[string]struct{})
	claimed := make(map[string]struct{})
	var mu sync.Mutex
	cond := sync.NewCond(&mu)

	queue := NewQueue()
	queue.Enqueue([]string{seed})

	return &Crawler{
		MaxPages:   maxPages,
		Que:        queue,
		crawled:    crawled,
		claimed:    claimed,
		mu:         &mu,
		cond:       cond,
		IsShutdown: false,
	}
}

func (c *Crawler) Shutdown() {
	c.IsShutdown = true
	c.cond.Broadcast()
}

func (c *Crawler) Next() (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for {
		if c.IsShutdown {
			return "", false
		}

		if len(c.crawled) >= c.MaxPages {
			c.Shutdown()
			return "", false
		}

		if c.Que.Len() > 0 {
			url, _ := c.Que.Dequeue()
			if _, dup := c.claimed[url]; dup {
				continue
			}
			c.claimed[url] = struct{}{}
			c.ActiveWorkers++
			return url, true
		}

		if c.ActiveWorkers == 0 {
			c.Shutdown()
			return "", false
		}

		c.cond.Wait()
	}
}

func (c *Crawler) Fail(url string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.crawled[url] = struct{}{}
	c.ActiveWorkers--
	c.TotalPages.Add(1)
}

func (c *Crawler) Done(url string, links []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.SuccessPages.Add(1)
	c.TotalPages.Add(1)

	c.crawled[url] = struct{}{}

	if len(c.crawled) >= c.MaxPages {
		c.Shutdown()
		return
	}

	if !c.IsShutdown {
		c.Que.Enqueue(links)
	}
	c.ActiveWorkers--
	c.cond.Broadcast()
}

type Latency struct {
	FetchMs    int
	ParseMs    int
	TotalMs    int
	PageSizeKB int
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

	crawler := NewCrawler(seed, maxPages)

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	done := make(chan struct{})

	start := time.Now()
	crawlerStats := CrawlerStats{StartTime: start}
	statsDone := make(chan struct{})
	go func(t *time.Ticker) {
		defer close(statsDone)
		for {
			select {
			case <-done:
				crawlerStats.update(crawler, time.Now())
				return

			case t := <-t.C:
				crawlerStats.update(crawler, t)
			}
		}
	}(ticker)

	var lwg sync.WaitGroup
	latencyChannel := make(chan Latency, maxPages)

	go func() {
		<-done
		close(latencyChannel)
	}()

	latencyBuckets := []int{1, 2, 5, 10, 15, 25, 50, 100, 250, 500, 1000, 2500, 5000, 10000, 15000, 20000, 30000, 60000, math.MaxInt}
	sizeBuckets := []int{25, 50, 75, 100, 250, 500, 750, 1000, 1500, 2000, 2500, 5000, math.MaxInt}

	fetchLatencyCounts := make([]int, len(latencyBuckets))
	parseLatencyCounts := make([]int, len(latencyBuckets))
	pageSizeCounts := make([]int, len(sizeBuckets))

	lwg.Add(1)
	go func() {
		defer lwg.Done()
		for latency := range latencyChannel {
			incCounts(latency.FetchMs, fetchLatencyCounts, latencyBuckets)
			incCounts(latency.ParseMs, parseLatencyCounts, latencyBuckets)
			incCounts(latency.PageSizeKB, pageSizeCounts, sizeBuckets)
		}
	}()

	workerWg := sync.WaitGroup{}
	for i := range 10 * runtime.NumCPU() {
		fmt.Printf("Worker %d started\n", i)
		workerWg.Add(1)
		go func() {
			defer workerWg.Done()
			worker(crawler, done, latencyChannel)
		}()
	}
	workerWg.Wait()

	close(done)
	<-statsDone
	lwg.Wait()
	totalTime := time.Since(start)
	fmt.Printf("crawled %d pages, total time: %.2fs\n", len(crawler.crawled), totalTime.Seconds())
	fmt.Printf("Success: %d\n", crawler.SuccessPages.Load())

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

func worker(crawler *Crawler, done chan struct{}, latencyChannel chan Latency) {

	for {
		rawUrl, ok := crawler.Next()
		if !ok {
			return
		}

		var latency Latency

		start := time.Now()

		resp, err := fetch(rawUrl)
		if err != nil {
			fetchTime := time.Since(start)
			totalTime := time.Since(start)
			totalTimeString := fmt.Sprintf("total %dms | fetch %dms", totalTime.Milliseconds(), fetchTime.Milliseconds())
			fmt.Printf("ERR %s: %v (%s)\n", rawUrl, err, totalTimeString)
			crawler.Fail(rawUrl)
			continue
		}

		base := resp.Request.URL
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()

		fetchTime := time.Since(start)
		latency.FetchMs = int(fetchTime.Milliseconds())

		if err != nil {
			totalTime := time.Since(start)
			totalTimeString := fmt.Sprintf("total %dms | fetch %dms", totalTime.Milliseconds(), fetchTime.Milliseconds())
			fmt.Printf("ERR %s: %v (%s)\n", rawUrl, err, totalTimeString)
			crawler.Fail(rawUrl)
			continue
		}

		pageSizeKB := int(len(body) / 1024)
		latency.PageSizeKB = int(len(body) / 1024)

		resp.Body = io.NopCloser(bytes.NewReader(body))
		links, parseTime := parser.Parse(resp, base)
		latency.ParseMs = int(parseTime.Milliseconds())

		totalTime := time.Since(start)
		latency.TotalMs = int(totalTime.Milliseconds())
		timeSpent := fmt.Sprintf("total %dms | fetch %dms | parse %dms | size %dKB | %d links", totalTime.Milliseconds(), fetchTime.Milliseconds(), parseTime.Milliseconds(), pageSizeKB, len(links))

		fmt.Printf("OK %s (%s)\n", rawUrl, timeSpent)

		latencyChannel <- latency

		crawler.Done(rawUrl, links)

	}
}

func fetch(rawURL string) (*http.Response, error) {
	resp, err := httpClient.Get(rawURL)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	return resp, nil
}
