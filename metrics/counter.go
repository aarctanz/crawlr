package metrics

import "sync/atomic"

type Counter struct {
	Queued  atomic.Int64
	Claimed atomic.Int64
	Crawled atomic.Int64
	Success atomic.Int64
	HTTP429 atomic.Int64
}

func (m *Counter) Queue(t int64) {
	m.Queued.Add(t)
}

func (m *Counter) Claim() {
	m.Claimed.Add(1)
}

func (m *Counter) ErrHTTP429() {
	m.HTTP429.Add(1)
}

func (m *Counter) Complete(ok bool) {
	m.Crawled.Add(1)
	if ok {
		m.Success.Add(1)
	}
}

func (m *Counter) Snapshot() (queued, claimed, crawled, success, http429, active int64) {
	q, c, d, s, h := m.Queued.Load(), m.Claimed.Load(), m.Crawled.Load(), m.Success.Load(), m.HTTP429.Load()
	return q, c, d, s, h, c - d
}
