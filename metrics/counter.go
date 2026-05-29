package metrics

import "sync/atomic"

type Counter struct {
	Claimed atomic.Int64
	Crawled atomic.Int64
	Success atomic.Int64
}

func (m *Counter) Claim() {
	m.Claimed.Add(1)
}

func (m *Counter) Complete(ok bool) {
	m.Crawled.Add(1)
	if ok {
		m.Success.Add(1)
	}
}

func (m *Counter) Snapshot() (claimed, crawled, success, active int64) {
	c, d, s := m.Claimed.Load(), m.Crawled.Load(), m.Success.Load()
	return c, d, s, c - d
}
