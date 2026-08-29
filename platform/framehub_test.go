package platform

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestFrameHubFanOut(t *testing.T) {
	h := NewFrameHub()
	h.SetCameraID("cam-1")

	var a, b atomic.Int64
	require.NoError(t, h.Subscribe("a", func(pts int64, au [][]byte) { a.Add(1) }))
	require.NoError(t, h.Subscribe("b", func(pts int64, au [][]byte) { b.Add(1) }))

	h.Broadcast(100, [][]byte{{0x65, 0x01}}, true)
	h.Broadcast(200, [][]byte{{0x41, 0x01}}, false)

	require.Eventually(t, func() bool { return a.Load() == 2 && b.Load() == 2 },
		2*time.Second, 5*time.Millisecond, "both consumers must receive both frames")
}

func TestFrameHubDuplicateSubscribe(t *testing.T) {
	h := NewFrameHub()
	require.NoError(t, h.Subscribe("dup", func(pts int64, au [][]byte) {}))
	err := h.Subscribe("dup", func(pts int64, au [][]byte) {})
	require.Error(t, err)
}

func TestFrameHubUnsubscribeStopsDelivery(t *testing.T) {
	h := NewFrameHub()
	var n atomic.Int64
	require.NoError(t, h.Subscribe("c", func(pts int64, au [][]byte) { n.Add(1) }))
	h.Broadcast(1, [][]byte{{1}}, true)
	require.Eventually(t, func() bool { return n.Load() == 1 }, 2*time.Second, 5*time.Millisecond)

	h.Unsubscribe("c")
	h.Broadcast(2, [][]byte{{1}}, true)
	time.Sleep(50 * time.Millisecond)
	require.EqualValues(t, 1, n.Load(), "no frames after Unsubscribe")
	// Unsubscribing an unknown consumer is a no-op.
	h.Unsubscribe("never-subscribed")
}

func TestFrameHubDropOnFull(t *testing.T) {
	h := NewFrameHub()
	block := make(chan struct{})
	inflight := make(chan struct{}, 1)
	var got atomic.Int64
	require.NoError(t, h.Subscribe("slow", func(pts int64, au [][]byte) {
		got.Add(1)
		select {
		case inflight <- struct{}{}:
		default:
		}
		<-block // consumer blocks: queue fills, further frames drop
	}))
	// Frame 0 lands in the callback; wait until it is provably in-flight so
	// the arithmetic below is deterministic (no scheduling race).
	h.Broadcast(0, [][]byte{{1}}, false)
	<-inflight
	// 200 more: 150 queued (capacity) + 50 dropped.
	for i := 1; i <= 200; i++ {
		h.Broadcast(int64(i), [][]byte{{1}}, false)
	}
	require.EqualValues(t, 50, h.Dropped())
	close(block)
	require.Eventually(t, func() bool { return got.Load() == 151 }, 2*time.Second, 5*time.Millisecond)
	require.EqualValues(t, 50, h.Dropped())
}
