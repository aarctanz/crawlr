package frontier

import "time"

type readyHost struct {
	ready    time.Time
	hostname string
}

type readyHostHeap []readyHost

func (h readyHostHeap) Len() int { return len(h) }

func (h readyHostHeap) Less(i, j int) bool { return h[j].ready.After(h[i].ready) }

func (h readyHostHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

func (h *readyHostHeap) Push(x any) {
	*h = append(*h, x.(readyHost))
}

func (h *readyHostHeap) Pop() any {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[0 : n-1]
	return x
}
