# crawlr

A concurrent web crawler written in Go. Each version of the crawler is measured and written up to show how a design choice changes its speed and memory use.

---

Starting from a seed URL, crawlr downloads each page, pulls out its links, and adds the new ones to a queue. It keeps going until it reaches the page limit or runs out of links, recording its speed and memory use as it goes.

## Getting started

You need Go 1.26 or newer.

```bash
git clone https://github.com/aarctanz/crawlr.git
cd crawlr
go run main.go https://example.com <max-pages>  # go run main.go https://example.com 15000
```

Crawlr logs each fetched url with its metrics to the stdout. After completion, it prints a summary and writes a `stats.json` file holding the full run timeline, sampled every five seconds.

---

Each version is documented in [`docs/README.md`](docs/README.md), with benchmarks and memory usage details.
