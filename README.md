# crawlr

A concurrent web crawler written in Go. Each version of the crawler is measured and written up to show how a design choice changes its speed and memory use.

---

Starting from one or more seed URLs, crawlr downloads each page, pulls out its links, and adds the new ones to a queue. It keeps going until it hits the page limit, the time limit, or runs out of links, recording its speed and memory use as it goes.

## Getting started

You need Go 1.26 or newer.

```bash
git clone https://github.com/aarctanz/crawlr.git
cd crawlr

# Default values
go run ./cmd/crawlr

# single seed, stop after 15000 pages
go run ./cmd/crawlr -url https://example.com -pages 15000

# seed from a file, stop after 30 minutes, 40 workers
go run ./cmd/crawlr -seeds seeds.txt -duration 30 -workers 40
```

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-url <url>` | — | A single seed URL. |
| `-seeds <file>` | — | Path to a seed file, one URL per line. Blank lines and lines starting with `#` are ignored. |
| `-pages <n>` | `1000` | Stop after crawling this many pages. must be greater than 0.|
| `-duration <dur>` | `No Limit` | Stop after this much wall-clock time in minutes, e.g. `30`, `10`. Must be greater than 0. |
| `-workers <n>` | `4 * NumCPU` | Number of worker goroutines. Must be greater than 0. |

If neither url nor seed file is provided, crawler starts with `https://crawler-test.com/`.

If both -url and -seeds provided, -seeds take priority.

`-pages` and `-duration` can be combined; the crawl stops on whichever is reached first. If neither is set, it runs until the frontier drains.

Crawlr logs each fetched url with its metrics to stdout. After completion, it prints a summary and writes `out/stats.json` holding the full run timeline, sampled every five seconds.

---

Each version is documented in [`docs/README.md`](docs/README.md), with benchmarks and memory usage details.
