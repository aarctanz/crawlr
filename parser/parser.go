package parser

import (
	"fmt"
	"net/http"
	"net/url"
	"time"

	"golang.org/x/net/html"
)

func Parse(resp *http.Response, base *url.URL) ([]string, string) {
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
