package frontier

import (
	"sync"
	"time"

	"golang.org/x/time/rate"
)

type Frontier struct {
	crawled       map[string]struct{}
	claimed       map[string]struct{}
	mu            *sync.Mutex
	cond          *sync.Cond
	MaxPages      int
	ActiveWorkers int
	IsShutdown    bool
	Que           *Queue

	Limiter *rate.Limiter
}

func NewFrontier(seed string, maxPages int) *Frontier {
	crawled := make(map[string]struct{})
	claimed := make(map[string]struct{})
	var mu sync.Mutex
	cond := sync.NewCond(&mu)

	queue := NewQueue()
	queue.Enqueue([]string{seed})

	return &Frontier{
		MaxPages:   maxPages,
		Que:        queue,
		crawled:    crawled,
		claimed:    claimed,
		mu:         &mu,
		cond:       cond,
		IsShutdown: false,
		Limiter:    rate.NewLimiter(rate.Every(500*time.Millisecond), 5),
	}
}

func (f *Frontier) Shutdown() {
	f.IsShutdown = true
	f.cond.Broadcast()
}

func (f *Frontier) Next() (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for {
		if f.IsShutdown {
			return "", false
		}

		if len(f.crawled) >= f.MaxPages {
			f.Shutdown()
			return "", false
		}

		if f.Que.Len() > 0 {
			url, _ := f.Que.Dequeue()
			if _, dup := f.claimed[url]; dup {
				continue
			}
			f.claimed[url] = struct{}{}
			f.ActiveWorkers++
			return url, true
		}

		if f.ActiveWorkers == 0 {
			f.Shutdown()
			return "", false
		}

		f.cond.Wait()
	}
}

func (f *Frontier) Fail(url string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.crawled[url] = struct{}{}
	f.ActiveWorkers--

	if len(f.crawled) >= f.MaxPages {
		f.Shutdown()
		f.cond.Broadcast()
	}
}

func (f *Frontier) Done(url string, links []string) int {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.ActiveWorkers--
	f.crawled[url] = struct{}{}

	if len(f.crawled) >= f.MaxPages {
		f.Shutdown()
		return 0
	}

	t := 0
	if !f.IsShutdown {
		f.Que.Enqueue(links)
		t = len(links)
	}
	f.cond.Broadcast()
	return t
}
