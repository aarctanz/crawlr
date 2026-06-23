# Stage 6 — Per-host rate limiting (polite *and* parallel)

Commit: `a34c12b` ("per-domain rate limiting via a host scheduler"); run on current code (bytes metric from `5caa45a`).

## What changed from Stage 5

Stage 5's global limiter throttled the *whole crawler* to one host's polite rate. This stage moves the limit to where it belongs: **each host gets its own clock.**

- **`HostsScheduler` + `readyHostHeap`** — a single scheduler goroutine owns the frontier. Each host carries a "ready time"; the heap is a min-heap on that time. The scheduler pops the soonest-ready host, dispatches one URL from its `Queue`, and pushes the host back with `ready = now + crawlDelay`.
- **`crawlDelay` = 10 s per host** — a host is never hit twice inside 10 s, but *different* hosts run concurrently. 
- **Links grouped by host** (`map[string][]string`) so the scheduler can enqueue per-host without re-parsing.
- **`BytesFetched` metric** — total bytes pulled, to measure throughput in bandwidth.

Run config: **180 workers, 10 s per-host delay, 1000+ URL seed list.**

## What the run shows

| Metric | Value |
|---|---|
| pages crawled | 20,179 |
| successes | 12,565 |
| success rate | 62% |
| HTTP 429s | **83** |
| wall time | 655 s (~10.9 min) |
| **pages/min** | **1,848** |
| success/min | 1,151 |
| total fetched | 2,294 MB (2.24 GB) |
| **sustained throughput** | **3.50 MB/s (~28 Mbit/s)** |
| page size (median / mean) | 80 KB / 116 KB |
| peak goroutines | 4,098 |
| peak heap (MB) | 4,444 |
| peak queued | 1,829,167 |

## It fixes both failure modes from Stages 4–5

| | Stage 4 (no limit) | Stage 5 (global limit) | Stage 6 (per-host) |
|---|---|---|---|
| HTTP 429s | 17,958 | 154 | **83** |
| success rate | 48% | 88% | 62% |
| pages/min | ~69,000 | 120 | **1,848** |
| active workers (of pool) | 2,000 / 2,000 | 1–6 / 40 | **180 / 180** |


- **Polite like Stage 5**: 83 `429`s across 20k pages.
- **Not pinned like Stage 5**: 1,848 pages/min is **~15× the global limiter's 120/min**, because many hosts run in parallel rather than sharing one bucket.
- **Workers stay busy**: `ActiveWorkers` sits pinned at **180/180** for the whole run. In Stage 5 only 1–6 of 40 workers were ever active (the rest parked on the global bucket). 
- 
## The ceiling is bandwidth, not workers

The `BytesFetched` metric exposes the run pulls **2.24 GB at a flat ~3.50 MB/s (~28 Mbit/s)** — the link is saturated for the whole run. With 180 workers all pinned active, the pipe — not the pool — sets the rate.

The throughput identity makes this concrete: **pages/min ≈ (bytes/s) ÷ (page size)**. Here 3.50 MB/s ÷ 116 KB ≈ 31 pages/s ≈ 1,850 pages/min — which is the measured rate. Worker count does not appear in that equation; workers only have to be numerous enough to keep the pipe full. Past that point, adding workers moves zero extra bytes — it just adds connection goroutines, contention, and frontier bloat. 

This also explains the 62% success rate despite only 83 `429`s: the ~7,600 failures are **not** server pushback. With the link saturated they are requests killed at the **10 s client timeout** (`httpClient.Timeout`, `cmd/crawlr/main.go:26`) — see the latency note below.

## Latency profile — where the time goes

From the same run (`metrics.LatencyMetrics`, n=12,565 successes):

| stage | p50 | p95 | p99 |
|---|---|---|---|
| fetch | 4,263 ms | 9,357 ms | 9,872 ms |
| parse | **0 ms** | 4 ms | 9 ms |
| total | 4,264 ms | 9,358 ms | 9,872 ms |

Two things fall straight out:

- **Parse is free; the crawler is 100% I/O-bound.** Total latency ≈ fetch latency to the millisecond. Parsing costs p50 **1 ms**, p99 9 ms — nothing (65% of parses finish under 1 ms). 

- **The histogram is right-censored at 10s.** In the current run `lm.Record` runs only on the full success path, every failure path `continue`s without recording. A fetch that exceeds `httpClient.Timeout = 10s` returns `context deadline exceeded` from `httpClient.Get` (`main.go:213–218`) and is dropped before it can be timed.

- **Measurement gap:** because failures aren't timed, the latency view is blind to the slowest ~38% of work. The fix is to record latency (and an outcome tag) on the failure paths too. This lines up with the roadmap's *error-type segregation* item.

Page sizes (successes): p50 **80 KB**, p95 723 KB, p99 1,676 KB, tail past 5 MB. The distribution is right-skewed, so the 116 KB mean above sits well above the 80 KB median; large pages over a saturated link are what produce the multi-second fetches and the timeout casualties.

## Still open: unbounded frontier

Peak heap **4.4 GB**, backlog **1.83M URLs** — both up sharply from earlier per-host runs, because more workers and a 1000+ seed list discover links far faster than the bandwidth-capped consumer can drain them. The frontier slice just keeps growing. Per-host rate limiting solved politeness and parallelism; it does nothing for memory, and scaling the run makes the memory problem worse, not better. On-disk frontier / dedup (Persistence on the roadmap) is the next pressure point.

## The lesson

Rate limiting belongs per-host. Done that way the crawler is polite *and* keeps every worker busy, instead of trading one for the other. And once politeness is solved, the bandwidth metric reveals the next true ceiling — throughput tracks the pipe, not the pool — which is the point of the design log: each stage's fix makes the *next* bottleneck measurable.
