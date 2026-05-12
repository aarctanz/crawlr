package main

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"

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

	visited := make(map[string]struct{})
	queue := []string{seed}

	for len(queue) > 0 && len(visited) < maxPages {
		current := queue[0]
		queue = queue[1:]
		if _, ok := visited[current]; ok {
			continue
		}
		visited[current] = struct{}{}

		links, err := fetch(current)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERR %s: %v\n", current, err)
			continue
		}
		fmt.Printf("OK %s: %d links\n", current, len(links))
		for _, link := range links {
			if _, ok := visited[link]; !ok {
				queue = append(queue, link)
			}
		}
	}
	fmt.Printf("visited %d pages\n", len(visited))
}

func fetch(rawUrl string) ([]string, error) {
	resp, err := http.Get(rawUrl)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP error: %d", resp.StatusCode)
	}

	base, err := url.Parse(resp.Request.URL.String())
	if err != nil {
		return nil, err
	}

	return parser(resp, base)
}

func parser(resp *http.Response, base *url.URL) ([]string, error) {

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
	return links, nil

}
