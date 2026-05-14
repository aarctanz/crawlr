package main

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/net/html"
)

type CrawlerStats struct {
	Sample    []TimeSeriesSample
	StartTime time.Time
}

type TimeSeriesSample struct {
	ElapsedS   int64
	PagesTotal int
	Success    int
	HeapMB     float64
	Goroutines int
}

func (c *CrawlerStats) update(crawled map[string]struct{}, mu *sync.Mutex, currentTime time.Time, success *atomic.Int32) {
	var s TimeSeriesSample
	s.ElapsedS = int64(currentTime.Sub(c.StartTime).Seconds())

	mu.Lock()
	s.PagesTotal = len(crawled)
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
				crawlerStats.update(crawled, &mu, time.Now(), &success)
				return

			case t := <-t.C:
				crawlerStats.update(crawled, &mu, t, &success)
			}
		}
	}(ticker)

	wg.Add(1)
	go fetch(seed, crawled, claimed, &mu, &wg, maxPages, &success)
	wg.Wait()
	done <- true
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
}

func fetch(rawUrl string, crawled map[string]struct{}, claimed map[string]struct{}, mu *sync.Mutex, wg *sync.WaitGroup, maxPages int, success *atomic.Int32) {
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

	timeSpent := fmt.Sprintf("fetch %dms", fetchTime.Milliseconds())
	if err != nil {
		totalTime := time.Since(start)
		timeSpent = fmt.Sprintf("total %dms | %s", totalTime.Milliseconds(), timeSpent)
		fmt.Printf("ERR %s: %v (%s)\n", rawUrl, err, timeSpent)
		mu.Lock()
		crawled[rawUrl] = struct{}{}
		mu.Unlock()
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		totalTime := time.Since(start)
		timeSpent = fmt.Sprintf("total %dms | %s", totalTime.Milliseconds(), timeSpent)
		fmt.Printf("ERR %s: HTTP error: %d (%s)\n", rawUrl, resp.StatusCode, timeSpent)
		mu.Lock()
		crawled[rawUrl] = struct{}{}
		mu.Unlock()
		return
	}

	base, err := url.Parse(resp.Request.URL.String())
	if err != nil {
		totalTime := time.Since(start)
		timeSpent = fmt.Sprintf("total %dms | %s", totalTime.Milliseconds(), timeSpent)
		fmt.Printf("ERR %s: %v (%s)\n", rawUrl, err, timeSpent)
		mu.Lock()
		crawled[rawUrl] = struct{}{}
		mu.Unlock()
		return
	}

	links, parseTime := parser(resp, base)
	timeSpent = fmt.Sprintf("%s | %s", timeSpent, parseTime)
	totalTime := time.Since(start)
	timeSpent = fmt.Sprintf("total %dms | %s", totalTime.Milliseconds(), timeSpent)
	success.Add(1)
	fmt.Printf("OK %s (%s)\n", rawUrl, timeSpent)

	mu.Lock()
	crawled[rawUrl] = struct{}{}
	mu.Unlock()

	for _, link := range links {
		wg.Add(1)
		go fetch(link, crawled, claimed, mu, wg, maxPages, success)
	}
}

func parser(resp *http.Response, base *url.URL) ([]string, string) {
	now := time.Now()

	z := html.NewTokenizer(resp.Body)
	var links []string

	for {
		tt := z.Next()

		if tt == html.ErrorToken {
			break
		}

		if tt == html.StartTagToken {
			tagName, _ := z.TagName()
			tag := string(tagName)

			if tag == "a" {
				for {
					k, v, more := z.TagAttr()
					if string(k) == "href" {
						link, err := base.Parse(string(v))
						if err != nil {
							break
						}
						links = append(links, link.String())
					}
					if !more {
						break
					}
				}
			}
		}
	}
	totalTime := time.Since(now)
	timeSpent := fmt.Sprintf("parse %dms", totalTime.Milliseconds())
	return links, timeSpent

}
