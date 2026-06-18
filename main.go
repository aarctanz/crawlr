package main

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strconv"
	"sync"
	"time"

	"github.com/aarctanz/crawlr/frontier"
	"github.com/aarctanz/crawlr/metrics"
	"github.com/aarctanz/crawlr/parser"
)

var httpClient = &http.Client{
	Timeout: 10 * time.Second,
}

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintf(os.Stderr, "usage: crawlr <seed-url> <max-pages> [num-workers]\n")
		os.Exit(1)
	}

	seed := os.Args[1]
	maxPages, err := strconv.Atoi(os.Args[2])
	if err != nil || maxPages <= 0 {
		fmt.Fprintf(os.Stderr, "max-pages must be a strictly positive integer\n")
		os.Exit(1)
	}
	seedURL, err := url.Parse(seed)
	if err != nil {
		fmt.Fprintf(os.Stderr, "seed-url must be a valid URL\n")
		os.Exit(1)
	}
	var numWorkers int
	if len(os.Args) >= 4 {
		numWorkers, err = strconv.Atoi(os.Args[3])
		if err != nil {
			fmt.Fprintf(os.Stderr, "num-workers must be an integer\n")
			os.Exit(1)
		}
	} else {
		numWorkers = 20 * runtime.NumCPU()
	}
	f := frontier.NewFrontier(numWorkers, seed, seedURL.Host, uint64(maxPages), 500*time.Millisecond)
	go f.HostsScheduler()
	crawlMetrics := metrics.Counter{}

	sampler := metrics.NewSampler(&crawlMetrics)
	go sampler.Run()

	lm := metrics.NewLatencyMetrics(maxPages)
	go lm.Run()

	workerWg := sync.WaitGroup{}
	for i := range numWorkers {
		fmt.Printf("Worker %d started\n", i)
		workerWg.Add(1)
		go func() {
			defer workerWg.Done()
			worker(f, lm, &crawlMetrics)
		}()
	}

	workerWg.Wait()
	lm.WaitAndClose()
	sampler.WaitAndClose()

	fmt.Printf("\n\n████ Crawler Stats ████\n\n")
	fmt.Println(lm.Report())

	totalTime := time.Since(sampler.StartTime)
	fmt.Printf("crawled %d pages, total time: %.2fs\n", crawlMetrics.Crawled.Load(), totalTime.Seconds())
	fmt.Printf("Success: %d\n", crawlMetrics.Success.Load())

	file, err := os.OpenFile("stats.json", os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		fmt.Println(err)
		return
	}
	defer file.Close()
	err = sampler.EncodeTo(file)
	if err != nil {
		fmt.Println(err)
	}
}

func worker(f *frontier.Frontier, lm *metrics.LatencyMetrics, crawlerMetrics *metrics.Counter) {

	for {
		rawUrl, ok := f.Next()
		if !ok {
			return
		}
		crawlerMetrics.Claim()
		var pageLatency metrics.PageLatency

		start := time.Now()

		resp, err := httpClient.Get(rawUrl)
		if err != nil {
			fmt.Printf("ERR %s: %v (%dms)\n", rawUrl, err, time.Since(start).Milliseconds())
			f.Fail(rawUrl)
			crawlerMetrics.Complete(false)
			continue
		}

		if resp.StatusCode != http.StatusOK {
			if resp.StatusCode == http.StatusTooManyRequests {
				crawlerMetrics.ErrHTTP429()
			}
			resp.Body.Close()
			fmt.Printf("ERR %s: HTTP %d (%dms)\n", rawUrl, resp.StatusCode, time.Since(start).Milliseconds())
			f.Fail(rawUrl)
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
			f.Fail(rawUrl)
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

		t := f.Done(rawUrl, links)
		crawlerMetrics.Complete(true)
		crawlerMetrics.Queue(int64(t))

	}
}
