# crawlr — design log

One file per stage of the crawler. Each records **what changed**, **why**, and a **stats summary** from a real run.

## Stages

| # | Stage | Doc | Commit |
|---|-------|-----|--------|
| 1 | Sequential crawler | [01-sequential.md](01-sequential.md) | `5bf4f65` |
| 2 | Concurrent — buffered channel | [02-buffered-channel.md](02-buffered-channel.md) | `90fb362` |
| 3 | Concurrent — slice-backed unbounded queue | [03-slice-queue.md](03-slice-queue.md) | `35018b2` |
| 4 | Cranking workers — the HTTP 429 wall | [04-http429-pushback.md](04-http429-pushback.md) | `2656acd` |
| 5 | Global rate limiter (wrong granularity) | [05-global-rate-limit.md](05-global-rate-limit.md) | `b18cd27` |

## Headline comparison

On same machine.

| Metric | sequential | buffered-channel | slice-queue | 2k-workers (429) | global-limit |
|---|---|---|---|---|---|
| pages crawled | 1,000 | 15,000 | 15,079 | 51,999 | 6,999 |
| wall time | 938 s | 116 s | 146 s | ~45 s | 3,497 s |
| **pages/min** | 64 | **7,826** | **6,197** | **~69,000** | 120 |
| success/min | 63 | 6,658 | 5,130 | ~33,000 | 106 |
| success rate | ~98% | — | — | **48%** | 88% |
| HTTP 429s | 0 | — | — | **17,958** | 154 |
| **peak goroutines** | 22 | **11,726** | **631** | 5,799 | 2,053 |
| peak heap (MB) | 46 | 1074 | 673 | 1,229 | 144 |

Stages 4–5 explore the **politeness / throughput trade**. Stage 4 maxes throughput (2,000 workers, no limit) and pays with a 48% success rate as hosts return `429`. Stage 5 over-corrects with one global limiter — polite (154 `429`s) but pinned at the bucket rate (120/min). Per-host rate limiting (current `HostsScheduler`) is the resolution.

Raw time-series for each run lives in `docs/data/<stage>/stats.json` (sampled every 5 s), with `goroutines.png`, `pages_crawled.png`, `heap_mb.png` rendered alongside.


## Run config

- Machine: 20 logical threads (10 physical cores)
- Workers: 4 × `runtime.NumCPU()` *(note: committed `cmd/crawlr/main.go` spawns `runtime.NumCPU()`; the benchmark runs used a 4× multiplier — set this when reproducing)*
- `max-pages`: 1,000 (sequential), 15,000 (concurrent)
- Stats sampled every 5 s by a ticker goroutine; see `CrawlerStats.update`.

## Method note

Each run writes `out/stats.json`; copy it into `docs/data/<stage>/stats.json` to preserve it as design-log evidence. The runs are nondeterministic (link discovery order varies), so success *rate* isn't directly comparable across stages — compare throughput, goroutines, and heap.

## Roadmap

Principle: every feature needs a measured reason. Order is rough priority; later items are conditional on earlier results.

**Done**
- [x] Sequential crawler
- [x] Concurrent fetch via buffered channel
- [x] Slice-backed unbounded frontier queue (`head`-index compaction)
- [x] Per-host rate limiting — `HostsScheduler` + `readyHostHeap`, `crawlDelay` spacing
- [x] URL normalization + per-host link grouping in parser
- [x] Metrics — atomic counters, 5s sampler → `stats.json`, latency histograms
- [x] Context & Cancellation — Timeout or Forceful shutdown by controller.
- [x] Graceful shutdown — Complete in-flight requests, write metrics in case of `ctrl+c`

**Next**
- [ ] Worker utilization metric — fraction of wall-clock blocked on `Next()` vs. fetching. Gates scale work: to find out if the crawler is host-rate-limited (scale pointless) or throughput-bound.
- [ ] Custom User-Agent + more refined `http.Client` — timeouts, connection pooling. 
- [ ] Error type segregation — typed errors (DNS, timeouts, non-2xx, parse) so `Fail()` can branch. Needed for retry/backoff.
- [ ] Per-host concurrency semaphore — burst cap, distinct from `crawlDelay` (spacing).
- [ ] robots.txt — respectful crawling.
- [ ] Pipeline — Decompose into stages, fetch -> parser -> dedup and enque.
- [ ] Persistence — crash-recovery/resume, on-disk dedup, output storage.
- [ ] Scale — multi machine crawling.
