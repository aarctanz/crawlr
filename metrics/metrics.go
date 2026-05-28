package metrics

import "sync/atomic"

type Metrics struct {
	Claimed atomic.Int64
	Crawled atomic.Int64
	Success atomic.Int64
}

func (m *Metrics) Claim() {
	m.Claimed.Add(1)
}

func (m *Metrics) Complete(ok bool) {
	m.Crawled.Add(1)
	if ok {
		m.Success.Add(1)
	}
}

func (m *Metrics) Snapshot() (claimed, crawled, success, active int64) {
	c, d, s := m.Claimed.Load(), m.Crawled.Load(), m.Success.Load()
	return c, d, s, c - d
}
