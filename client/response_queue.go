package tcClient

import "sync"

// responseQueue lets the gRPC receive loop return to Recv immediately while
// XML decoding and delivery to typed consumers continue in FIFO order.
type responseQueue struct {
	mu       sync.Mutex
	messages []string
	notify   chan struct{}
	closed   bool
}

func newResponseQueue() *responseQueue {
	return &responseQueue{notify: make(chan struct{}, 1)}
}

func (q *responseQueue) push(message string) bool {
	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		return false
	}
	q.messages = append(q.messages, message)
	q.mu.Unlock()
	q.signal()
	return true
}

func (q *responseQueue) pop(done <-chan struct{}) (string, bool) {
	for {
		select {
		case <-done:
			return "", false
		default:
		}

		q.mu.Lock()
		if len(q.messages) != 0 {
			message := q.messages[0]
			q.messages[0] = ""
			q.messages = q.messages[1:]
			if len(q.messages) == 0 {
				q.messages = nil
			}
			q.mu.Unlock()
			return message, true
		}
		closed := q.closed
		q.mu.Unlock()
		if closed {
			return "", false
		}

		select {
		case <-q.notify:
		case <-done:
			return "", false
		}
	}
}

func (q *responseQueue) close() {
	q.mu.Lock()
	q.closed = true
	q.mu.Unlock()
	q.signal()
}

func (q *responseQueue) signal() {
	select {
	case q.notify <- struct{}{}:
	default:
	}
}
