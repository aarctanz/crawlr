# Stage 5 — Global rate limiter (the wrong shape of polite)

Commit: `b18cd27` ("Global rate limiting fetching")

## What changed from Stage 4

Stage 4 proved that hammering hosts wastes half the crawl on `429`s. The first, naive fix: a **single global rate limiter** in front of every fetch — a token bucket capped at **120 requests/min (2 req/s)** shared across *all* workers and *all* hosts. Workers block on the limiter before each request.

Worker pool set to 40 for this run.

## What the run shows

| Metric | Value |
|---|---|
| pages crawled | 6,999 |
| successes | 6,173 |
| HTTP 429s | 154 |
| wall time | 3,497 s (~58 min) |
| **pages/min** | **120.1** |
| success/min | ~106 |
| peak goroutines | 2,053 |
| peak heap (MB) | 144 |
| peak queued | 763,628 |

The politeness goal is met: only **154** `429`s across the whole run (vs 17,958 in Stage 4), and a 88% success rate. Servers are happy.

## But the throughput is the cap, exactly

```
 elapsed   crawled   active workers
  500s      1,003          1
 1000s      2,003          1
 1500s      3,002          2
 2000s      3,998          6
 3000s      6,003          1
```

Pages/min lands at **120.1** — i.e. precisely the 2 req/s bucket. That is the whole story: the global limiter makes the *entire crawler* run at the speed of a single polite client. It's **~50× slower** than the Stage 3 slice-queue run and ~575× slower (gross) than Stage 4.

The `ActiveWorkers` column shows why it's wasteful: 1–6 workers active out of 40 at any sample. The other ~35 sit parked on the token bucket doing nothing. Worker count is irrelevant — you could have 2 workers or 2,000; the global bucket pins total throughput at 2 req/s regardless.

(The ~2,000 baseline goroutines that persist through the run are idle keep-alive/transport goroutines across the many distinct hosts in the frontier, not active workers — the active set never exceeds 6.)

## The flaw: politeness is per-host, the limiter is global

Rate limiting exists to avoid overloading *a host*. But the limit a single host can tolerate has nothing to do with how many *other* hosts you're crawling. With 763k queued URLs spread across thousands of hosts, the polite per-host rate (say 2 req/s/host) would allow thousands of req/s in aggregate. The global bucket throws all that parallelism away — it throttles host A's budget because host B was just visited.

A global limiter is the right idea (space out requests) applied at the wrong granularity (the whole crawler instead of each host).

## The lesson

The fix needs to be **per-host**: each host gets its own delay/clock, and the scheduler runs as many hosts in parallel as it has work for. That is the next stage — `HostsScheduler` + `readyHostHeap`, where each host carries its own "ready time" and the crawler stays both polite *and* fast.
