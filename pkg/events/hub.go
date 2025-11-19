package events

import (
	"encoding/json"
	"sync"
)

type EventHub struct {
	mu   sync.RWMutex
	subs map[chan Event]struct{}
}

func NewEventHub() *EventHub { return &EventHub{subs: make(map[chan Event]struct{})} }

func (h *EventHub) Subscribe() chan Event {
	ch := make(chan Event, 16)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	return ch
}

func (h *EventHub) Unsubscribe(ch chan Event) {
	h.mu.Lock()
	if _, ok := h.subs[ch]; ok {
		delete(h.subs, ch)
		close(ch)
	}
	h.mu.Unlock()
}

func (h *EventHub) Publish(name string, payload any) {
	if h == nil {
		return
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return
	}
	msg := Event{Name: name, Data: b}
	h.mu.RLock()
	for ch := range h.subs {
		// Non-blocking send; drop if subscriber is slow
		select {
		case ch <- msg:
		default:
		}
	}
	h.mu.RUnlock()
}
