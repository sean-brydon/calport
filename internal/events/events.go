// Package events is the one event model shared by the box, the laptop agent,
// and hooks. Every event records the tool it came from, so an integration
// never reacts to its own changes and bounces them back and forth.
package events

import (
	"sync"
	"time"
)

type Event struct {
	Type   string         `json:"type"`
	Time   time.Time      `json:"time"`
	Box    string         `json:"box,omitempty"`
	Origin string         `json:"origin,omitempty"`
	Error  string         `json:"error,omitempty"`
	Data   map[string]any `json:"data,omitempty"`
}

// Bus fans events out to subscribers. A subscriber that falls behind loses
// events rather than stalling the publisher.
type Bus struct {
	mu   sync.Mutex
	subs map[chan Event]struct{}
	Now  func() time.Time
}

func (b *Bus) Subscribe() (<-chan Event, func()) {
	ch := make(chan Event, 64)
	b.mu.Lock()
	if b.subs == nil {
		b.subs = map[chan Event]struct{}{}
	}
	b.subs[ch] = struct{}{}
	b.mu.Unlock()
	return ch, func() {
		b.mu.Lock()
		delete(b.subs, ch)
		b.mu.Unlock()
	}
}

// Publish stamps e with the current time unless it already has one.
func (b *Bus) Publish(e Event) {
	if e.Time.IsZero() {
		if b.Now != nil {
			e.Time = b.Now()
		} else {
			e.Time = time.Now()
		}
	}
	if e.Origin == "" {
		e.Origin = "calport"
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subs {
		select {
		case ch <- e:
		default:
		}
	}
}
