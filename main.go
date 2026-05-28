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
	"time"

	"github.com/aarctanz/crawlr/metrics"
	"github.com/aarctanz/crawlr/parser"
)

type CrawlerStats struct {
	Sample    []TimeSeriesSample
	StartTime time.Time
}

type TimeSeriesSample struct {
	ElapsedS      int64
	Crawled       int64
	Claimed       int64
	Success       int64
	ActiveWorkers int64
	HeapMB        float64
	Goroutines    int
}

func (c *CrawlerStats) update(met *metrics.Metrics, currentTime time.Time) {
	var s TimeSeriesSample
	s.ElapsedS = int64(currentTime.Sub(c.StartTime).Seconds())

	s.Claimed, s.Crawled, s.Success, s.ActiveWorkers = met.Snapshot()

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
	MaxPages      int
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

	if len(c.crawled) >= c.MaxPages {
		c.Shutdown()
		c.cond.Broadcast()
	}
}

func (c *Crawler) Done(url string, links []string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.ActiveWorkers--
	c.crawled[url] = struct{}{}

	if len(c.crawled) >= c.MaxPages {
		c.Shutdown()
		return
	}

	if !c.IsShutdown {
		c.Que.Enqueue(links)
	}
	c.cond.Broadcast()
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
	crawlMetrics := metrics.Metrics{}

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
				crawlerStats.update(&crawlMetrics, time.Now())
				return

			case t := <-t.C:
				crawlerStats.update(&crawlMetrics, t)
			}
		}
	}(ticker)

	lm := metrics.NewLatencyMetrics(maxPages)
	go lm.UpdateCounts()

	workerWg := sync.WaitGroup{}
	for i := range runtime.NumCPU() {
		fmt.Printf("Worker %d started\n", i)
		workerWg.Add(1)
		go func() {
			defer workerWg.Done()
			worker(crawler, lm, &crawlMetrics)
		}()
	}
	workerWg.Wait()

	close(done)
	lm.WaitAndClose()
	fmt.Println(lm.Report())

	<-statsDone

	totalTime := time.Since(start)
	fmt.Printf("crawled %d pages, total time: %.2fs\n", crawlMetrics.Crawled.Load(), totalTime.Seconds())
	fmt.Printf("Success: %d\n", crawlMetrics.Success.Load())

	file, err := os.OpenFile("stats.json", os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		fmt.Println(err)
		return
	}
	defer file.Close()
	encoder := json.NewEncoder(file)
	encoder.Encode(crawlerStats)
}

func worker(crawler *Crawler, lm *metrics.LatencyMetrics, crawlerMetrics *metrics.Metrics) {

	for {
		rawUrl, ok := crawler.Next()
		if !ok {
			return
		}
		crawlerMetrics.Claim()
		var pageLatency metrics.PageLatency

		start := time.Now()

		resp, err := fetch(rawUrl)
		if err != nil {
			fetchTime := time.Since(start)
			totalTime := time.Since(start)
			totalTimeString := fmt.Sprintf("total %dms | fetch %dms", totalTime.Milliseconds(), fetchTime.Milliseconds())
			fmt.Printf("ERR %s: %v (%s)\n", rawUrl, err, totalTimeString)
			crawler.Fail(rawUrl)
			crawlerMetrics.Complete(false)
			continue
		}

		base := resp.Request.URL
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()

		fetchTime := time.Since(start)
		pageLatency.FetchMs = int(fetchTime.Milliseconds())

		if err != nil {
			totalTime := time.Since(start)
			totalTimeString := fmt.Sprintf("total %dms | fetch %dms", totalTime.Milliseconds(), fetchTime.Milliseconds())
			fmt.Printf("ERR %s: %v (%s)\n", rawUrl, err, totalTimeString)
			crawler.Fail(rawUrl)
			crawlerMetrics.Complete(false)
			continue
		}

		pageLatency.PageSizeKB = int(len(body) / 1024)

		resp.Body = io.NopCloser(bytes.NewReader(body))
		links, parseTime := parser.Parse(resp, base)
		pageLatency.ParseMs = int(parseTime.Milliseconds())

		totalTime := time.Since(start)
		pageLatency.TotalMs = int(totalTime.Milliseconds())
		timeSpent := fmt.Sprintf("total %dms | fetch %dms | parse %dms | size %dKB | %d links", totalTime.Milliseconds(), fetchTime.Milliseconds(), parseTime.Milliseconds(), pageLatency.PageSizeKB, len(links))

		fmt.Printf("OK %s (%s)\n", rawUrl, timeSpent)

		lm.Record(pageLatency)

		crawler.Done(rawUrl, links)
		crawlerMetrics.Complete(true)

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
