package main

import (
	"bufio"
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/aarctanz/crawlr/internal/frontier"
	"github.com/aarctanz/crawlr/internal/metrics"
	"github.com/aarctanz/crawlr/internal/parser"
)

var httpClient = &http.Client{
	Timeout: 10 * time.Second,
}

func main() {
	const defaultSeed = "https://crawler-test.com/"

	var (
		seedsFile = flag.String("seeds", "", "path to a file of seed URLs, one per line (# comments and blank lines ignored)")
		singleURL = flag.String("url", "", "a single seed URL (-seeds takes priority if both are set)")
		maxPages  = flag.Uint64("pages", 1000, "stop after crawling this many pages (must be > 0)")
		durMins   = flag.Int("duration", 0, "stop after this many minutes of wall-clock time (0 = no limit)")
		workers   = flag.Int("workers", 0, "number of worker goroutines (0 = 4 * NumCPU)")
	)
	flag.Parse()

	if *maxPages == 0 {
		fmt.Fprintf(os.Stderr, "pages must be > 0\n")
		os.Exit(1)
	}
	if *durMins < 0 {
		fmt.Fprintf(os.Stderr, "duration must be > 0\n")
		os.Exit(1)
	}
	if *workers < 0 {
		fmt.Fprintf(os.Stderr, "workers must be > 0\n")
		os.Exit(1)
	}

	// Seed source: -seeds takes priority over -url when both are given. If
	// neither is given, fall back to the default seed.
	var seeds map[string][]string
	var err error
	switch {
	case *seedsFile != "":
		seeds, err = readSeeds(*seedsFile)
	case *singleURL != "":
		seeds, err = parseSeed(*singleURL)
	default:
		seeds, err = parseSeed(defaultSeed)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	if len(seeds) == 0 {
		fmt.Fprintf(os.Stderr, "no valid seed URLs found\n")
		os.Exit(1)
	}

	numWorkers := *workers
	if numWorkers == 0 {
		numWorkers = 4 * runtime.NumCPU()
	}

	f := frontier.NewFrontier(numWorkers, seeds, *maxPages, 500*time.Millisecond)
	go f.HostsScheduler()
	crawlMetrics := metrics.Counter{}

	sampler := metrics.NewSampler(&crawlMetrics)
	go sampler.Run()

	lm := metrics.NewLatencyMetrics(numWorkers)
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

	// Time-based termination: stop after -duration minutes (whichever comes
	// first relative to -pages). Skipped entirely when duration == 0.
	if *durMins > 0 {
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(*durMins)*time.Minute)
		defer cancel()
		go func() {
			<-ctx.Done()
			if ctx.Err() == context.DeadlineExceeded {
				fmt.Println("Duration reached, shutting down...")
				f.Shutdown()
			}
		}()
	}

	// Graceful shutdown in case of ctrl + c
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-signalChan
		fmt.Println("Received termination signal, shutting down...")
		signal.Stop(signalChan)
		f.Shutdown()
	}()

	workerWg.Wait()
	lm.WaitAndClose()
	sampler.WaitAndClose()

	fmt.Printf("\n\n████ Crawler Stats ████\n\n")
	fmt.Println(lm.Report())

	totalTime := time.Since(sampler.StartTime)
	fmt.Printf("crawled %d pages, total time: %.2fs\n", crawlMetrics.Crawled.Load(), totalTime.Seconds())
	fmt.Printf("Success: %d\n", crawlMetrics.Success.Load())

	if err := os.MkdirAll("out", 0755); err != nil {
		fmt.Println(err)
		return
	}
	file, err := os.OpenFile("out/stats.json", os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
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

// parseSeed validates a single raw URL and returns it grouped by host.
func parseSeed(raw string) (map[string][]string, error) {
	host, normalized, err := parseLine(raw)
	if err != nil {
		return nil, err
	}
	return map[string][]string{host: {normalized}}, nil
}

// readSeeds reads a seed file (one URL per line; blank lines and lines
// starting with # are ignored) and returns the URLs grouped by host.
func readSeeds(path string) (map[string][]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	seeds := make(map[string][]string)
	scanner := bufio.NewScanner(file)
	for line := 1; scanner.Scan(); line++ {
		raw := strings.TrimSpace(scanner.Text())
		if raw == "" || strings.HasPrefix(raw, "#") {
			continue
		}
		host, perHost, err := parseLine(raw)
		if err != nil {
			return nil, fmt.Errorf("%s line %d: %w", path, line, err)
		}
		seeds[host] = append(seeds[host], perHost)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return seeds, nil
}

// parseLine validates one seed URL, returning its host and the URL itself.
func parseLine(raw string) (host, normalized string, err error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", "", fmt.Errorf("invalid URL %q: %w", raw, err)
	}
	if u.Scheme == "" || u.Host == "" {
		return "", "", fmt.Errorf("URL %q must be absolute (scheme + host)", raw)
	}
	return u.Host, raw, nil
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
		crawlerMetrics.AddBytes(int64(len(body)))

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
