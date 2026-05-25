# Stage 3 — Concurrent crawler, slice-backed unbounded queue

Commit: `35018b2` ("moved to slice backed unbounded queue from buffered channel")

## What changed from Stage 2

Replaced the channel-as-queue with an explicit data structure and a `sync.Cond` for coordination.

- **`Queue`**: a slice-backed FIFO — `append` to enqueue, a `head` index to dequeue, and compaction (`copy` down) when `head > len/2` so it doesn't grow without bound. O(1) amortized both ends.
- **`NextUrl`** — cond-var consumer: returns a URL if the queue is non-empty, otherwise `cond.Wait()`s; shuts the whole crawler down when the queue is drained **and** `ActiveWorkers == 0` (no one can produce more).
- **`PushUrls`** — locked producer that enqueues and `Broadcast`s.
- **`ActiveWorkers int`** replaces the WaitGroup: termination needs `Que.Len() == 0 && ActiveWorkers == 0` checked **together under one lock**.

## Why move off the channel

Stage 2's speed came from storing the backlog in parked goroutine stacks (one `insert` goroutine per page), so goroutine count grew unbounded. An explicit slice queue holds the same backlog in a compact `[]string` with a fixed worker pool — the backlog lives in memory you can measure and bound, not in the scheduler.

## Stats summary

| Metric | Value |
|---|---|
| pages crawled | 15,079 |
| wall time | 146 s |
| pages/min | 6,197 |
| success/min | 5,130 |
| peak goroutines | 631 |
| peak heap (MB) | 673 |

Goroutine count over time — **flat / bounded**:

```
5s:205  10s:264  15s:275  20s:304  25s:307  30s:319  35s:393  40s:420 ... ~631 peak
```

The worker pool is fixed; the variance is transient HTTP-client goroutines, not backlog. (The +79 over the 15,000 cap: workers already past the `MaxPages` check before `Shutdown` broadcast — harmless overshoot.)

## The trade vs Stage 2

| | buffered-channel | slice-queue |
|---|---|---|
| pages/min | 7,759 | 6,197 (−20%) |
| peak goroutines | 11,750 (grows) | 631 (flat) |
| peak heap (MB) | 741 | 673 |
| backlog stored as | parked goroutine stacks | `[]string` |
| scales to large crawls | no (goroutines unbounded) | yes (bounded) |

Slower hot path — every queue op **and** every `crawled`/`claimed` map op serializes on one mutex, plus the `cond.Wait` loop — but goroutines and memory stay bounded and stable regardless of crawl size. This is the version to build on.
