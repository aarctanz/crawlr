package main

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
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
	queue := []string{seed}

	for len(queue) > 0 && len(visited) < maxPages {
		current := queue[0]
		queue = queue[1:]
		if _, ok := visited[current]; ok {
			continue
		}
		visited[current] = struct{}{}

		now := time.Now()
		links, err, timeSpent := fetch(current)
		if err != nil {
			totalTime := time.Since(now)
			timeSpent = fmt.Sprintf("total %dms | %s", totalTime.Milliseconds(), timeSpent)
			fmt.Fprintf(os.Stderr, "ERR %s: %v (%s)\n", current, err, timeSpent)
			continue
		}
		totalTime := time.Since(now)
		timeSpent = fmt.Sprintf("total %dms | %s", totalTime.Milliseconds(), timeSpent)
		fmt.Printf("OK %s: %d links (%s)\n", current, len(links), timeSpent)
		for _, link := range links {
			if _, ok := visited[link]; !ok {
				queue = append(queue, link)
			}
		}
	}
	totalTime := time.Since(start)
	fmt.Printf("visited %d pages, total time: %.2fs\n", len(visited), totalTime.Seconds())
}

func fetch(rawUrl string) ([]string, error, string) {
	now := time.Now()
	resp, err := http.Get(rawUrl)
	totalTime := time.Since(now)
	timeSpent := fmt.Sprintf("fetch %dms", totalTime.Milliseconds())
	if err != nil {
		return nil, err, timeSpent
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP error: %d", resp.StatusCode), timeSpent
	}

	base, err := url.Parse(resp.Request.URL.String())
	if err != nil {
		return nil, err, timeSpent
	}

	links, err, parseTime := parser(resp, base)
	timeSpent = fmt.Sprintf("%s | %s", timeSpent, parseTime)
	return links, err, timeSpent
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
