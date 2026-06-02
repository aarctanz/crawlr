package parser

import (
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"time"

	"golang.org/x/net/html"
)

var (
	URLRegex = regexp.MustCompile(`[(http(s)?):\/\/(www\.)?a-zA-Z0-9@:%._\+~#=]{2,256}\.[a-z]{2,6}\b([-a-zA-Z0-9@:%_\+.~#?&//=]*)`)
)

func verifyValidURL(rawUrl string, base *url.URL) (*url.URL, error) {
	parsedUrl, err := base.Parse(rawUrl)
	if err != nil {
		return nil, err
	}
	if URLRegex.MatchString(parsedUrl.String()) {
		return parsedUrl, nil
	}

	return nil, errors.New("Invalid Url")
}

func Parse(resp *http.Response, base *url.URL) ([]string, time.Duration) {
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
						link, err := verifyValidURL(string(v), base)
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
	return links, totalTime

}
