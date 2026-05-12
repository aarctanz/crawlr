package main

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"sync"
	"time"

	"golang.org/x/net/html"
)

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

	start := time.Now()

	visited := make(map[string]struct{})
	var mu sync.Mutex
	var wg sync.WaitGroup

	wg.Add(1)
	go fetch(seed, visited, &mu, &wg, maxPages)
	wg.Wait()
	totalTime := time.Since(start)
	fmt.Printf("visited %d pages, total time: %.2fs\n", len(visited), totalTime.Seconds())
}

func fetch(rawUrl string, visited map[string]struct{}, mu *sync.Mutex, wg *sync.WaitGroup, maxPages int) {
	defer wg.Done()

	mu.Lock()
	if len(visited) >= maxPages {
		mu.Unlock()
		return
	}

	if _, ok := visited[rawUrl]; ok {
		mu.Unlock()
		return
	}
	visited[rawUrl] = struct{}{}
	mu.Unlock()

	start := time.Now()
	resp, err := http.Get(rawUrl)
	fetchTime := time.Since(start)

	timeSpent := fmt.Sprintf("fetch %dms", fetchTime.Milliseconds())
	if err != nil {
		totalTime := time.Since(start)
		timeSpent = fmt.Sprintf("total %dms | %s", totalTime.Milliseconds(), timeSpent)
		fmt.Printf("ERR %s: %v (%s)\n", rawUrl, err, timeSpent)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		totalTime := time.Since(start)
		timeSpent = fmt.Sprintf("total %dms | %s", totalTime.Milliseconds(), timeSpent)
		fmt.Printf("ERR %s: HTTP error: %d (%s)\n", rawUrl, resp.StatusCode, timeSpent)
		return
	}

	base, err := url.Parse(resp.Request.URL.String())
	if err != nil {
		totalTime := time.Since(start)
		timeSpent = fmt.Sprintf("total %dms | %s", totalTime.Milliseconds(), timeSpent)
		fmt.Printf("ERR %s: %v (%s)\n", rawUrl, err, timeSpent)
		return
	}

	links, err, parseTime := parser(resp, base)
	timeSpent = fmt.Sprintf("%s | %s", timeSpent, parseTime)
	totalTime := time.Since(start)
	timeSpent = fmt.Sprintf("total %dms | %s", totalTime.Milliseconds(), timeSpent)
	fmt.Printf("OK %s (%s)\n", rawUrl, timeSpent)

	for _, link := range links {
		wg.Add(1)
		go fetch(link, visited, mu, wg, maxPages)
	}
}

func parser(resp *http.Response, base *url.URL) ([]string, error, string) {
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
	return links, nil, timeSpent

}
