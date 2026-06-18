package frontier

import (
	"container/heap"
	"sync"
	"time"
)

type Frontier struct {
	claimed map[string]struct{}
	crawled uint64

	mu   *sync.Mutex
	cond *sync.Cond

	maxPages      uint64
	activeWorkers int
	isShutdown    bool

	queues      map[string]*Queue
	queuedCount int

	crawlDelay   time.Duration
	readyHosts   readyHostHeap
	inReadyHosts map[string]struct{}

	urlChan chan string
}

func NewFrontier(numWorkers int, seedURL string, seedHost string, maxPages uint64, crawlDelay time.Duration) *Frontier {
	claimed := make(map[string]struct{})
	var mu sync.Mutex
	cond := sync.NewCond(&mu)

	queue := NewQueue()
	queue.Enqueue([]string{seedURL})
	readyHosts := readyHostHeap{}
	heap.Init(&readyHosts)
	heap.Push(&readyHosts, readyHost{hostname: seedHost, ready: time.Now()})

	return &Frontier{
		maxPages:     maxPages,
		queues:       map[string]*Queue{seedHost: queue},
		claimed:      claimed,
		mu:           &mu,
		cond:         cond,
		isShutdown:   false,
		urlChan:      make(chan string, numWorkers*2),
		queuedCount:  1,
		readyHosts:   readyHosts,
		inReadyHosts: make(map[string]struct{}),
		crawlDelay:   crawlDelay,
	}
}

func (f *Frontier) Shutdown() {
	f.isShutdown = true
	f.cond.Broadcast()
}

func (f *Frontier) Next() (string, bool) {
	f.mu.Lock()

	if f.isShutdown {
		f.mu.Unlock()
		return "", false
	}

	if f.crawled >= f.maxPages {
		f.Shutdown()
		f.mu.Unlock()
		return "", false
	}
	f.mu.Unlock()

	url, ok := <-f.urlChan
	if !ok {
		return "", false
	}
	return url, true
}

func (f *Frontier) Fail(url string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.crawled += 1
	f.activeWorkers--
	if f.crawled >= f.maxPages {
		f.Shutdown()
	}
	f.cond.Broadcast()
}

func (f *Frontier) Done(url string, links map[string][]string) int {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.activeWorkers--
	f.crawled += 1

	if f.crawled >= f.maxPages {
		f.Shutdown()
		return 0
	}

	t := 0
	if !f.isShutdown {
		for host, urls := range links {
			if _, ok := f.queues[host]; !ok {
				f.queues[host] = NewQueue()
			}
			f.queues[host].Enqueue(urls)
			if _, ok := f.inReadyHosts[host]; !ok {
				f.inReadyHosts[host] = struct{}{}
				heap.Push(&f.readyHosts, readyHost{hostname: host, ready: time.Now()})
			}
			t += len(urls)
		}
		f.queuedCount += t
	}
	f.cond.Broadcast()
	return t
}

func (f *Frontier) HostsScheduler() {
	for {
		f.mu.Lock()
		if f.isShutdown {
			close(f.urlChan)
			f.mu.Unlock()
			return
		}

		if len(f.readyHosts) == 0 {
			if f.activeWorkers == 0 && f.queuedCount == 0 {
				f.Shutdown()
				f.mu.Unlock()
				continue
			}
			f.cond.Wait()
			f.mu.Unlock()
			continue
		}

		top := f.readyHosts[0]
		if top.ready.After(time.Now()) {
			f.mu.Unlock()
			time.Sleep(time.Until(top.ready))
			continue
		}

		rh := heap.Pop(&f.readyHosts).(readyHost)
		url, ok := f.queues[rh.hostname].Dequeue()
		if !ok {
			delete(f.queues, rh.hostname)
			delete(f.inReadyHosts, rh.hostname)
			f.mu.Unlock()
			continue
		}

		if _, dup := f.claimed[url]; dup {
			f.queuedCount--
			if f.queues[rh.hostname].Len() == 0 {
				delete(f.queues, rh.hostname)
				delete(f.inReadyHosts, rh.hostname)
			} else {
				heap.Push(&f.readyHosts, readyHost{hostname: rh.hostname, ready: time.Now()})
			}
			f.mu.Unlock()
			continue
		}

		if f.queues[rh.hostname].Len() == 0 {
			delete(f.queues, rh.hostname)
			delete(f.inReadyHosts, rh.hostname)
		} else {
			heap.Push(&f.readyHosts, readyHost{hostname: rh.hostname, ready: time.Now().Add(f.crawlDelay)})
		}

		f.claimed[url] = struct{}{}
		f.activeWorkers++
		f.queuedCount--
		f.mu.Unlock()
		f.urlChan <- url
	}
}
