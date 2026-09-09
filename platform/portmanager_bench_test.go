package platform

// PortManager allocation benchmarks (issue #45): serial and concurrent
// Get/Recycle — the per-session cost of media-port bookkeeping.

import "testing"

func BenchmarkPortManagerGetRecycle(b *testing.B) {
	pm := NewPortManager(30000, 30099)
	b.ResetTimer()
	for range b.N {
		p, err := pm.Get()
		if err != nil {
			b.Fatal(err)
		}
		pm.Recycle(p)
	}
}

func BenchmarkPortManagerGetRecycleParallel(b *testing.B) {
	pm := NewPortManager(30000, 30099)
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			p, err := pm.Get()
			if err != nil {
				b.Error(err)
				return
			}
			pm.Recycle(p)
		}
	})
}
