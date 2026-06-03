package frontier

type Queue struct {
	queue []string
	head  int
}

func NewQueue() *Queue {
	return &Queue{
		head:  0,
		queue: make([]string, 0, 4096),
	}
}

func (q *Queue) Enqueue(urls []string) {
	q.queue = append(q.queue, urls...)
}

func (q *Queue) Dequeue() (string, bool) {
	if q.head == len(q.queue) {
		return "", false
	}

	url := q.queue[q.head]
	q.queue[q.head] = ""
	q.head += 1
	if q.head > len(q.queue)/2 {
		n := copy(q.queue, q.queue[q.head:])
		q.queue = q.queue[:n]
		q.head = 0
	}
	return url, true
}

func (q *Queue) Len() int {
	return len(q.queue) - q.head
}
