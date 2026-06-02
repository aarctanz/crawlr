package parser

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/html"
)

// accepts url only if it is an http(s) link with a host.
func verifyValidURL(rawUrl string, base *url.URL) (*url.URL, error) {
	u, err := base.Parse(rawUrl)
	if err != nil {
		return nil, err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("unsupported scheme %q", u.Scheme)
	}
	if u.Host == "" {
		return nil, errors.New("missing host")
	}
	return u, nil
}

// strips the #fragment.
func removeFragment(u *url.URL) {
	u.Fragment = ""
	u.RawFragment = ""
}

// canonicalises query-param order so ?a=1&b=2 and ?b=2&a=1 produce one URL.
func sortURLQueryParams(u *url.URL) {
	if u.RawQuery == "" {
		return
	}
	u.RawQuery = u.Query().Encode()
}

// applies the RFC 3986 syntax-based normalizations: lowercase scheme and host,
// drop the default port, give the root an explicit "/" path, strip the fragment,
// and sort query params.
func normalizeURL(u *url.URL) {
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)

	// Drop the redundant default port. Hostname() strips the port and the
	// IPv6 brackets, so put the brackets back for IPv6 literals.
	if (u.Scheme == "http" && u.Port() == "80") || (u.Scheme == "https" && u.Port() == "443") {
		host := u.Hostname()
		if strings.Contains(host, ":") {
			host = "[" + host + "]"
		}
		u.Host = host
	}

	if u.Path == "" {
		u.Path = "/"
	}

	removeFragment(u)
	sortURLQueryParams(u)
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
						if err == nil {
							normalizeURL(link)
							links = append(links, link.String())
						}
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
