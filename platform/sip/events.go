package sip

import (
	"context"
	"sync"
	"time"
)

// TopicGB28181Alarm is published when a GB/T 28181 device pushes an alarm
// notification (SUBSCRIBE Alarm / NOTIFY).
const TopicGB28181Alarm = "gb28181.alarm"

// Event is one published occurrence on a topic.
type Event struct {
	Topic string
	Data  interface{}
}

// GB28181AlarmEvent is the payload published on TopicGB28181Alarm.
type GB28181AlarmEvent struct {
	CameraID         string    `json:"camera_id,omitempty"`
	DeviceID         string    `json:"device_id"`
	ChannelID        string    `json:"channel_id,omitempty"`
	AlarmPriority    string    `json:"alarm_priority,omitempty"` // 1高 2中 3低
	AlarmMethod      string    `json:"alarm_method,omitempty"`   // 2 motion, 5 offline...
	AlarmType        string    `json:"alarm_type,omitempty"`
	AlarmTime        string    `json:"alarm_time,omitempty"`
	AlarmDescription string    `json:"alarm_description,omitempty"`
	ReceivedAt       time.Time `json:"received_at"`
}

type eventSubscriber struct {
	ch     chan Event
	mu     sync.Mutex // protects send vs close race
	closed bool
}

// EventBus is a minimal lossy pub/sub bus for platform events: Publish never
// blocks on a slow consumer (ring-overflow drops the oldest event), matching
// MiBeeNvr's event bus semantics. Hosts with their own bus adapt Publish.
type EventBus struct {
	mu          sync.RWMutex
	bufferSize  int
	subscribers map[string][]*eventSubscriber
}

// NewEventBus creates a bus with the given per-subscriber buffer size
// (<=0 defaults to 64).
func NewEventBus(bufferSize int) *EventBus {
	if bufferSize <= 0 {
		bufferSize = 64
	}
	return &EventBus{
		bufferSize:  bufferSize,
		subscribers: make(map[string][]*eventSubscriber),
	}
}

// Subscribe registers ch for topic events. The channel's capacity caps
// delivery; overflow drops the oldest queued event on the next publish.
func (b *EventBus) Subscribe(topic string, ch chan Event, _ int) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.subscribers[topic] = append(b.subscribers[topic], &eventSubscriber{ch: ch})
	return nil
}

// Unsubscribe removes ch from topic.
func (b *EventBus) Unsubscribe(topic string, ch chan Event) {
	b.mu.Lock()
	subs := b.subscribers[topic]
	filtered := subs[:0]
	for _, s := range subs {
		if s.ch == ch {
			s.mu.Lock()
			s.closed = true
			s.mu.Unlock()
		} else {
			filtered = append(filtered, s)
		}
	}
	b.subscribers[topic] = filtered
	b.mu.Unlock()
}

// Publish delivers evt to topic subscribers without ever blocking: a full
// subscriber queue drops its oldest event to make room.
func (b *EventBus) Publish(ctx context.Context, topic string, data any) {
	evt := Event{Topic: topic, Data: data}

	b.mu.RLock()
	subs := make([]*eventSubscriber, len(b.subscribers[topic]))
	copy(subs, b.subscribers[topic])
	b.mu.RUnlock()

	for _, s := range subs {
		select {
		case <-ctx.Done():
			return
		default:
		}

		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			continue
		}
		// Ring-buffer overflow: drain the oldest event to make room. The
		// cap>0 guard keeps unbuffered channels from deadlocking (#220 in
		// the source project).
		if cap(s.ch) > 0 && len(s.ch) == cap(s.ch) {
			select {
			case <-s.ch: // drop oldest
			default:
			}
		}
		select {
		case s.ch <- evt:
		default: // unbuffered-and-no-reader — drop newest
		}
		s.mu.Unlock()
	}
}
