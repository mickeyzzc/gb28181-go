package metrics_test

import (
	"fmt"
	"sync/atomic"

	"github.com/mickeyzzc/gb28181-go/device"
	"github.com/mickeyzzc/gb28181-go/metrics"
	"github.com/mickeyzzc/gb28181-go/platform"
	"github.com/mickeyzzc/gb28181-go/platform/sip"
)

// PrometheusBridge sketches the host-side bridge (issue #40): the
// interface lives in the library, the counters live with the consumer —
// swap the atomics for prometheus.CounterVec labels in a real setup and
// the library needs no metrics dependency.
type PrometheusBridge struct {
	registersOK atomic.Int64
	psBytes     atomic.Int64
}

func (b *PrometheusBridge) RegisterAttempt()      {}
func (b *PrometheusBridge) RegisterOK()           { b.registersOK.Add(1) }
func (b *PrometheusBridge) RegisterFail()         {}
func (b *PrometheusBridge) KeepaliveFail()        {}
func (b *PrometheusBridge) InviteSessionStarted() {}
func (b *PrometheusBridge) InviteSessionStopped() {}
func (b *PrometheusBridge) InviteFail()           {}
func (b *PrometheusBridge) PSBytesOut(n int64)    { b.psBytes.Add(n) }

var _ metrics.Hooks = (*PrometheusBridge)(nil)

// Example_hooks shows the wiring on both roles.
func Example_hooks() {
	bridge := &PrometheusBridge{}

	// Device (UAC): lifecycle + media bytes.
	srv := device.New(device.Config{}, device.DeviceInfo{}, nil)
	srv.SetMetricsHooks(bridge)

	// Platform (UAS): registration outcomes + INVITE sessions.
	plat := sip.NewServer(sip.Config{},
		platform.NewDeviceManager(0),
		platform.NewSessionManager(platform.NewPortManager(30000, 30050), ""), nil)
	plat.SetMetricsHooks(bridge)

	fmt.Println("bridged:", bridge.registersOK.Load() == 0)
	// Output: bridged: true
}
