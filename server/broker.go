package tcServer

import (
	"errors"
	"sync"
)

var errSlowSubscriber = errors.New("subscriber cannot keep up with connector messages")

type broker struct {
	mu          sync.Mutex
	nextID      uint64
	closed      bool
	subscribers map[uint64]*subscription
}

type subscription struct {
	id       uint64
	broker   *broker
	messages chan string
	done     chan struct{}
	err      error
}

func newBroker() *broker {
	return &broker{subscribers: make(map[uint64]*subscription)}
}

func (b *broker) subscribe(buffer int) *subscription {
	if buffer < 1 {
		buffer = 1
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.nextID++
	sub := &subscription{
		id:       b.nextID,
		broker:   b,
		messages: make(chan string, buffer),
		done:     make(chan struct{}),
	}
	if b.closed {
		close(sub.done)
		return sub
	}
	b.subscribers[sub.id] = sub
	return sub
}

// publish never blocks the TRANSAQ callback. A lagging subscriber is detached
// instead of stalling the vendor callback thread and every other client.
func (b *broker) publish(message string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	for id, sub := range b.subscribers {
		select {
		case sub.messages <- message:
		default:
			sub.err = errSlowSubscriber
			close(sub.done)
			delete(b.subscribers, id)
		}
	}
}

func (b *broker) close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.closed = true
	for id, sub := range b.subscribers {
		close(sub.done)
		delete(b.subscribers, id)
	}
}

func (s *subscription) close() {
	s.broker.mu.Lock()
	defer s.broker.mu.Unlock()
	if _, exists := s.broker.subscribers[s.id]; !exists {
		return
	}
	close(s.done)
	delete(s.broker.subscribers, s.id)
}

func (s *subscription) error() error {
	s.broker.mu.Lock()
	defer s.broker.mu.Unlock()
	return s.err
}
