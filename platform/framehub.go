package platform

import (
	"fmt"
	"sync"
	"sync/atomic"
)

// FrameCallback receives one demuxed video access unit (NALU list, 90kHz PTS).
type FrameCallback func(pts int64, au [][]byte)

// frameHubQueueSize bounds each consumer's delivery queue (~7.5s at 20fps).
// Frames delivered to a full queue are dropped — live preview prefers fresh
// frames over completeness.
const frameHubQueueSize = 150

// FrameHub fans demuxed access units out to named subscribers (recorders,
// preview, cascade forwarders). Each subscriber's callback runs on a dedicated
// drain goroutine, so a slow or blocking consumer never stalls Broadcast or
// the other consumers; overflow drops frames (drop-on-full) instead of
// blocking the RTP receive path.
//
// It replaces MiBeeNvr's streamhub.StreamHub at the library seam with the
// subset of semantics the GB28181 session layer relies on: named
// subscribe/unsubscribe, per-consumer delivery goroutine, bounded queue with
// drop-on-full. Hosts needing the full NVR feature set (IDR fast-start
// replay, jitter reorder, drop-rate telemetry) adapt Subscribe on their side.
type FrameHub struct {
	cameraID  string
	mu        sync.Mutex
	consumers map[string]*frameHubConsumer
	dropped   atomic.Int64
}

type frameHubConsumer struct {
	cb   FrameCallback
	ch   chan frameHubFrame
	done chan struct{}
}

type frameHubFrame struct {
	pts   int64
	au    [][]byte
	isIDR bool
}

// NewFrameHub creates an empty hub.
func NewFrameHub() *FrameHub {
	return &FrameHub{consumers: make(map[string]*frameHubConsumer)}
}

// SetCameraID tags the hub for logging on the host side.
func (h *FrameHub) SetCameraID(id string) { h.cameraID = id }

// CameraID returns the tag set by SetCameraID.
func (h *FrameHub) CameraID() string { return h.cameraID }

// Subscribe registers a consumer under a unique ID. The callback is invoked
// from a dedicated goroutine and may block without affecting other consumers
// or Broadcast. Returns an error if the ID is already subscribed.
func (h *FrameHub) Subscribe(id string, cb FrameCallback) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.consumers[id]; ok {
		return fmt.Errorf("consumer %q already subscribed", id)
	}
	c := &frameHubConsumer{
		cb:   cb,
		ch:   make(chan frameHubFrame, frameHubQueueSize),
		done: make(chan struct{}),
	}
	h.consumers[id] = c
	go c.drain()
	return nil
}

// Unsubscribe removes a consumer and stops its delivery goroutine. Unknown
// IDs are a no-op.
func (h *FrameHub) Unsubscribe(id string) {
	h.mu.Lock()
	c, ok := h.consumers[id]
	if ok {
		delete(h.consumers, id)
	}
	h.mu.Unlock()
	if ok {
		close(c.done)
	}
}

// Broadcast fans one access unit out to all consumers, non-blocking. Frames
// that do not fit a consumer's queue are dropped and counted in Dropped().
func (h *FrameHub) Broadcast(pts int64, au [][]byte, isIDR bool) {
	h.mu.Lock()
	consumers := make([]*frameHubConsumer, 0, len(h.consumers))
	for _, c := range h.consumers {
		consumers = append(consumers, c)
	}
	h.mu.Unlock()
	for _, c := range consumers {
		select {
		case c.ch <- frameHubFrame{pts: pts, au: au, isIDR: isIDR}:
		default:
			h.dropped.Add(1)
		}
	}
}

// Dropped reports the number of frames dropped due to full consumer queues.
func (h *FrameHub) Dropped() int64 { return h.dropped.Load() }

func (c *frameHubConsumer) drain() {
	for {
		select {
		case f := <-c.ch:
			c.cb(f.pts, f.au)
		case <-c.done:
			return
		}
	}
}
