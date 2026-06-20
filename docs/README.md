# crawlr — design log

One file per stage of the crawler. Each records **what changed**, **why**, and a **stats summary** from a real run.

## Stages

| # | Stage | Doc | Commit |
|---|-------|-----|--------|
| 1 | Sequential crawler | [01-sequential.md](01-sequential.md) | `5bf4f65` |
| 2 | Concurrent — buffered channel | [02-buffered-channel.md](02-buffered-channel.md) | `90fb362` |
| 3 | Concurrent — slice-backed unbounded queue | [03-slice-queue.md](03-slice-queue.md) | `35018b2` |

## Headline comparison

On same machine.

| Metric | sequential | buffered-channel | slice-queue |
|---|---|---|---|
| pages crawled | 1,000 | 15,000 | 15,079 |
| wall time | 938 s | 116 s | 146 s |
| **pages/min** | 64 | **7,826** | **6,197** |
| success/min | 63 | 6,658 | 5,130 |
| **peak goroutines** | 22 | **11,726** | **631** |
| peak heap (MB) | 46 | 1074 | 673 |

Raw time-series for each run lives in `data/<stage>/stats.json` (sampled every 5 s), with `goroutines.png`, `pages_crawled.png`, `heap_mb.png` rendered alongside.


## Run config

- Machine: 20 logical threads (10 physical cores)
- Workers: 4 × `runtime.NumCPU()` *(note: committed `main.go` spawns `runtime.NumCPU()`; the benchmark runs used a 4× multiplier — set this when reproducing)*
- `max-pages`: 1,000 (sequential), 15,000 (concurrent)
- Stats sampled every 5 s by a ticker goroutine; see `CrawlerStats.update`.

## Method note

Each run writes `stats.json` to the repo root; copy it into `data/<stage>/stats.json` before the next run or it gets overwritten. The runs are nondeterministic (link discovery order varies), so success *rate* isn't directly comparable across stages — compare throughput, goroutines, and heap.

## Roadmap

Principle: every feature needs a measured reason. Order is rough priority; later items are conditional on earlier results.

**Done**
- [x] Sequential crawler
- [x] Concurrent fetch via buffered channel
- [x] Slice-backed unbounded frontier queue (`head`-index compaction)
- [x] Per-host rate limiting — `HostsScheduler` + `readyHostHeap`, `crawlDelay` spacing
- [x] URL normalization + per-host link grouping in parser
- [x] Metrics — atomic counters, 5 s sampler → `stats.json`, latency histograms
- [x] Context & Cancellation — Timeout or Forceful shutdown by controller.
- [x] Graceful shutdown — write metrics in case of `ctrl+c`

**Next**
- [ ] Worker utilization metric — fraction of wall-clock blocked on `Next()` vs. fetching. Gates scale work: to find out if the crawler is host-rate-limited (scale pointless) or throughput-bound.
- [ ] Custom User-Agent + more refined `http.Client` — timeouts, connection pooling. 
- [ ] Error type segregation — typed errors (DNS, timeouts, non-2xx, parse) so `Fail()` can branch. Needed for retry/backoff.
- [ ] Per-host concurrency semaphore — burst cap, distinct from `crawlDelay` (spacing).
- [ ] robots.txt — respectful crawling.
- [ ] Pipeline — Decompose into stages, fetch -> parser -> dedup and enque.
- [ ] Persistence — crash-recovery/resume, on-disk dedup, output storage.
- [ ] Scale — multi machine crawling.
