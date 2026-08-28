// Test double implementing [FrameSource] with the same bounded-channel,
// drop-on-full semantics as the source project's capture hub — for this
// package's tests and for host test suites.

package device

import (
	"context"
	"sync"
)

// FrameHub is a reference [FrameSource]: fans out [AccessUnit]s to
// subscribers via bounded channels, dropping for slow consumers instead
// of blocking the producer.
type FrameHub struct {
	mu                   sync.Mutex
	subscribers          map[string]*FrameSubscription
	chans                map[string]chan AccessUnit
	cancels              map[string]context.CancelFunc
	nextID               int
	subscriberBufferSize int
}

// NewFrameHub creates a hub with the default subscriber buffer (64).
func NewFrameHub() *FrameHub { return NewFrameHubWithSize(64) }

// NewFrameHubWithSize creates a hub with the given subscriber buffer size.
func NewFrameHubWithSize(size int) *FrameHub {
	return &FrameHub{
		subscribers:          make(map[string]*FrameSubscription),
		chans:                make(map[string]chan AccessUnit),
		cancels:              make(map[string]context.CancelFunc),
		subscriberBufferSize: size,
	}
}

// Write distributes an access unit to all subscribers (non-blocking).
func (h *FrameHub) Write(au AccessUnit) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, ch := range h.chans {
		select {
		case ch <- au:
		default:
			// Subscriber too slow — drop frame.
		}
	}
}

// Subscribe registers a subscriber; it is removed when ctx is cancelled.
func (h *FrameHub) Subscribe(ctx context.Context) *FrameSubscription {
	h.mu.Lock()
	h.nextID++
	id := string(rune(h.nextID)) // simple unique ID, mirrors the source hub
	ctx, cancel := context.WithCancel(ctx)

	ch := make(chan AccessUnit, h.subscriberBufferSize)
	sub := &FrameSubscription{ID: id, Channel: ch}
	h.subscribers[id] = sub
	h.chans[id] = ch
	h.cancels[id] = cancel
	h.mu.Unlock()

	go func() {
		defer h.Unsubscribe(id)
		<-ctx.Done()
	}()
	return sub
}

// Unsubscribe removes a subscriber by ID and closes its channel.
func (h *FrameHub) Unsubscribe(id string) {
	h.mu.Lock()
	_, ok := h.subscribers[id]
	if !ok {
		h.mu.Unlock()
		return
	}
	delete(h.subscribers, id)
	ch := h.chans[id]
	delete(h.chans, id)
	cancel := h.cancels[id]
	delete(h.cancels, id)
	h.mu.Unlock()
	close(ch)
	if cancel != nil {
		cancel()
	}
}

// SubscriberCount returns the current number of active subscribers.
func (h *FrameHub) SubscriberCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subscribers)
}
