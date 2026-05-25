# Stage 1 — Sequential crawler

Commit: `5bf4f65` ("sequential crawler metrics, 1000 max pages")

## What it is

The baseline. A single goroutine pulls one URL, fetches it, parses links, pushes them onto a queue, repeats. No concurrency, no shared-state synchronization. Establishes the metrics harness (time-series sampling of pages/goroutines/heap every 5 s) that every later stage reuses.


## Stats summary

| Metric | Value |
|---|---|
| pages crawled | 1,000 |
| wall time | 938 s |
| pages/min | 64 |
| success/min | 63 |
| peak goroutines | 22 |
| peak heap (MB) | 46 |

Goroutine count (~22) is just the runtime + HTTP client internals; the crawler itself is one goroutine. Heap stays small because there's no backlog of parked work.
