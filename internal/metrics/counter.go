package metrics

import "sync/atomic"

type Counter struct {
	Queued       atomic.Int64
	Claimed      atomic.Int64
	Crawled      atomic.Int64
	Success      atomic.Int64
	HTTP429      atomic.Int64
	BytesFetched atomic.Int64
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

func (m *Counter) Snapshot() (queued, claimed, crawled, success, http429, active, bytesFetched int64) {
	q, c, d, s, h, b := m.Queued.Load(), m.Claimed.Load(), m.Crawled.Load(), m.Success.Load(), m.HTTP429.Load(), m.BytesFetched.Load()
	return q, c, d, s, h, c - d, b
}

func (m *Counter) AddBytes(b int64) {
	m.BytesFetched.Add(b)
}

func (m *Counter) BytesTotal() int64 {
	return m.BytesFetched.Load()
}
