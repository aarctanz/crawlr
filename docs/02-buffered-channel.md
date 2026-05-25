# Stage 2 — Concurrent crawler, buffered channel

Commit: `90fb362` ("bounded worker pool draining shared urlChannel")

## What changed from Stage 1

- A fixed pool of `NumCPU` worker goroutines, each ranging over a shared `urlChannel` (`chan string`).
- Shared `crawled` / `claimed` maps guarded by a `sync.Mutex`.
- Termination via a `sync.WaitGroup` counting outstanding URLs; a closer goroutine `close`s `urlChannel` once the count hits zero.

```go
urlChannel := make(chan string, 10,000)   
...
for i := range runtime.NumCPU() {
    go worker(urlChannel, ...)
}
```

## The key design problem

Workers are **both consumers and producers** — each crawled page yields new links that must go back on the queue. Pushing directly into a bounded channel deadlocks the moment the buffer fills: every worker is blocked on `urlChannel <- url`, so no one is left to receive. With a 10-slot buffer that happens almost immediately.

The workaround in this version: spawn a goroutine per page to do the pushing.

```go
wg.Add(1)
go insert(links, urlChannel, wg, done)   // one pusher goroutine per crawled page

func insert(urls []string, urlChannel chan string, ...) {
    for _, url := range urls {
        wg.Add(1)
        urlChannel <- url   // blocks here until a slot frees up
    }
}
```

Each `insert` goroutine parks on a blocked send until the channel drains. **So the queue backlog is stored as parked goroutine stacks, not as a data structure.** The "buffer" is really the Go scheduler's run queue.

## Stats summary

| Metric | Value |
|---|---|
| pages crawled | 15,000 |
| wall time | 116 s |
| pages/min | 7,759 |
| success/min | 6,658 |
| peak goroutines | 11,750 |
| peak heap (MB) | 741 |

Goroutine count over time — **linear, no plateau**:

```
5s:735  10s:1433  15s:2112  20s:2744  25s:3294  30s:3820  35s:4307 ... → 11,750
```

## Why it's fast

Workers never block while producing — pushing is offloaded to `insert` goroutines — cheaper synchronization of channel send/receive provided by the runtime.

## Note

Goroutine count grows linearly with pages crawled and never plateaus: ~11,750 at 15k pages, so ~117k at 150k pages. At ~8 KB minimum stack each, that's ~90 MB in stacks alone at 15k.
