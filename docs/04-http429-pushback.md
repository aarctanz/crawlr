# Stage 4 — Cranking workers, and the HTTP 429 wall

Commit: `2656acd` ("slice based queue stats with http 429 counter")

## What changed from Stage 3

No new queue or scheduler. Two changes:

- **Worker pool cranked to 2,000** (from `~NumCPU`-scale). The slice-queue from Stage 3 is bounded, so it can absorb a much larger worker count without the goroutine blow-up of Stage 2 — this stage tests what happens when you actually do it.
- **`HTTP429` counter added** to the metrics counter, alongside `Queued`. Until now "crawled" lumped successes and failures together. This stage needed to *see* the failure mode, so the counter splits out `429 Too Many Requests` — the signal a server sends when you hit it too hard.

There is still **no rate limiting**. Every worker fetches as fast as it can.

## What the run shows

52,000-page cap, 2,000 workers, no per-host delay.

| Metric | Value |
|---|---|
| pages crawled | 51,999 |
| successes | 24,776 |
| HTTP 429s | 17,958 |
| crawl wall time (cap reached) | ~45 s |
| gross pages/min | ~69,000 |
| **success/min** | **~33,000** |
| peak goroutines | 5,799 |
| peak heap (MB) | 1,229 |
| peak queued (frontier backlog) | 4,124,216 |

Throughput is enormous — ~69k pages/min, ~11× the Stage 3 slice-queue run — because 2,000 workers keep far more requests in flight. But that number is a lie: **less than half the fetches succeed.**

## The 429 wall

Watch successes and 429s diverge as servers start fighting back:

```
 elapsed   crawled   success    429    Δsuccess/Δcrawled (this interval)
   5s        3,214     3,090      12        96%
  15s       14,040    11,583   1,680        ~62%
  25s       24,756    15,635   5,791        ~38%
  35s       33,124    19,506   9,226        ~46%
  40s       49,056    23,364  16,893        ~24%
  45s       51,822    24,706  17,956        ~49% (tail, cap nearly hit)
```

Early on, 96% of fetches succeed. By 40 s the marginal success rate has collapsed to ~24% — for every 4 new pages fetched, 3 come back `429`. Cumulative success ends at **48%** (24,776 / 51,999). The crawler is spending half its work getting rejected.

The cause is structural: link discovery is bursty and locally dense — pages on one host link mostly to the same host. With no spacing between requests to a host and 2,000 workers draining a FIFO, the same host gets slammed by dozens of concurrent requests, and it starts shedding load with `429`.

## The memory cost too

Peak heap hits **1,229 MB** and peak queued backlog hits **4.1 million URLs**. With workers consuming at ~69k/min but link discovery producing far faster (each successful page adds many links), the frontier slice grows almost unbounded for the length of the run. Cranking workers makes the producer/consumer imbalance worse, not better.

## The lesson

More workers buys raw concurrency but not *useful* throughput once you exceed what hosts will tolerate. The 429 counter makes the ceiling visible: past some point, added workers just convert into rejected requests and frontier bloat.

This is the motivation for the next stage — **rate limiting** — so the crawler stops hammering hosts faster than they'll answer. Stage 5 tries the naive version (one global limiter) and shows why it's the wrong shape.
