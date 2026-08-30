package sip

// Second-round wire coverage for the MESSAGE handler branches (keepalive
// liveness incl. spoof/off-line paths, device-info merge, device status,
// alarm-over-MESSAGE, time-sync answer), the periodic catalog loop, and
// the startup-failure rollback.

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/ghettovoice/gosip/sip"
	"github.com/mickeyzzc/gb28181-go/manscdp"
	"github.com/mickeyzzc/gb28181-go/platform"
	"github.com/stretchr/testify/require"
)

// manscdpMessage builds a MESSAGE request carrying an encoded body.
func manscdpMessage(t *testing.T, cfg Config, client *sipClient, fromUser string, body any) sip.Request {
	t.Helper()

	encoded, err := manscdp.Encode(body)
	require.NoError(t, err)

	return buildRequest(t, sip.MESSAGE, fromUser, testServerID, cfg.SIPListen, client.localPort(), string(encoded))
}

func TestHandleMessageWireBranches(t *testing.T) {
	cfg := testConfig(t)
	_, dm := startTestServer(t, cfg)
	client := newSIPClient(t, cfg.SIPListen)

	dm.Register(&platform.Device{ID: testDeviceID, NetAddr: client.conn.LocalAddr().String()})
	dev, _ := dm.Device(testDeviceID)
	dev.Status.Store(platform.DeviceOnline)

	t.Run("empty body 400", func(t *testing.T) {
		res := client.roundTrip(buildRequest(t, sip.MESSAGE, testDeviceID, testServerID, cfg.SIPListen, client.localPort(), ""))
		require.Equal(t, 400, int(res.StatusCode()))
	})

	t.Run("invalid body 400", func(t *testing.T) {
		res := client.roundTrip(buildRequest(t, sip.MESSAGE, testDeviceID, testServerID, cfg.SIPListen, client.localPort(), "not-xml"))
		require.Equal(t, 400, int(res.StatusCode()))
	})

	t.Run("keepalive ok touches device", func(t *testing.T) {
		res := client.roundTrip(manscdpMessage(t, cfg, client, testDeviceID, manscdp.Keepalive{
			CmdType: manscdp.CmdKeepalive, SN: 31, DeviceID: testDeviceID, Status: "OK",
		}))
		require.Equal(t, 200, int(res.StatusCode()))
	})

	t.Run("keepalive sender mismatch 403", func(t *testing.T) {
		res := client.roundTrip(manscdpMessage(t, cfg, client, "34020000001319999999", manscdp.Keepalive{
			CmdType: manscdp.CmdKeepalive, SN: 32, DeviceID: testDeviceID, Status: "OK",
		}))
		require.Equal(t, 403, int(res.StatusCode()))
	})

	t.Run("keepalive unregistered 403", func(t *testing.T) {
		res := client.roundTrip(manscdpMessage(t, cfg, client, "34020000001329999999", manscdp.Keepalive{
			CmdType: manscdp.CmdKeepalive, SN: 33, DeviceID: "34020000001329999999", Status: "OK",
		}))
		require.Equal(t, 403, int(res.StatusCode()))
	})

	t.Run("keepalive off announces shutdown", func(t *testing.T) {
		res := client.roundTrip(manscdpMessage(t, cfg, client, testDeviceID, manscdp.Keepalive{
			CmdType: manscdp.CmdKeepalive, SN: 34, DeviceID: testDeviceID, Status: "OFF",
		}))
		require.Equal(t, 200, int(res.StatusCode()))

		require.Eventually(t, func() bool {
			d, ok := dm.Device(testDeviceID)
			return ok && d.Status.Load() != platform.DeviceOnline
		}, 3*time.Second, 20*time.Millisecond, "OFF keepalive must mark the device offline")
	})

	t.Run("device info merges fields", func(t *testing.T) {
		res := client.roundTrip(manscdpMessage(t, cfg, client, testDeviceID, manscdp.DeviceInfo{
			CmdType: manscdp.CmdDeviceInfo, SN: 35, DeviceID: testDeviceID,
			DeviceName: "Gate Cam", Manufacturer: "MiBee", Model: "Eye v2",
		}))
		require.Equal(t, 200, int(res.StatusCode()))

		require.Eventually(t, func() bool {
			d, ok := dm.Device(testDeviceID)
			if !ok {
				return false
			}
			d.Mu.RLock()
			defer d.Mu.RUnlock()
			return d.Name == "Gate Cam" && d.Manufacturer == "MiBee" && d.Model == "Eye v2"
		}, 3*time.Second, 20*time.Millisecond, "device info must merge into the registry")
	})

	t.Run("device status abnormal is tolerated", func(t *testing.T) {
		res := client.roundTrip(manscdpMessage(t, cfg, client, testDeviceID, manscdp.DeviceStatus{
			CmdType: manscdp.CmdDeviceStatus, SN: 36, DeviceID: testDeviceID,
			Status: "ERROR", Time: "2026-01-02T03:04:05",
		}))
		require.Equal(t, 200, int(res.StatusCode()))
	})

	t.Run("alarm over message", func(t *testing.T) {
		res := client.roundTrip(manscdpMessage(t, cfg, client, testDeviceID, manscdp.Alarm{
			CmdType: manscdp.CmdAlarm, SN: 37, DeviceID: testDeviceID,
			AlarmPriority: "2", AlarmMethod: "5", AlarmTime: "2026-01-02T03:04:05",
		}))
		require.Equal(t, 200, int(res.StatusCode()))
	})

	t.Run("time sync query answered", func(t *testing.T) {
		query := manscdpMessage(t, cfg, client, testDeviceID, manscdp.TimeSyncQuery{
			CmdType: manscdp.CmdTimeSync, SN: 38, DeviceID: testDeviceID,
		})
		_, err := client.conn.WriteToUDP([]byte(query.String()), client.addr)
		require.NoError(t, err)

		require.Eventually(t, func() bool {
			for {
				req := client.nextRequest(100 * time.Millisecond)
				if req == nil {
					return false
				}
				if req.Method() == sip.MESSAGE && strings.Contains(string(req.Body()), "<CmdType>TimeSync</CmdType>") {
					return strings.Contains(string(req.Body()), "<SN>38</SN>")
				}
			}
		}, 5*time.Second, 50*time.Millisecond, "platform must answer the clock query")
	})
}

func TestCatalogLoopQueriesDevices(t *testing.T) {
	cfg := testConfig(t)
	cfg.CatalogInterval = "200ms"

	pmm := platform.NewPortManager(30400, 30410)
	dm := platform.NewDeviceManager(60 * time.Second)
	sm := platform.NewSessionManager(pmm, cfg.ServerID)
	srv := NewServer(cfg, dm, sm, nil)
	require.NoError(t, srv.Start(context.Background()))
	t.Cleanup(func() { _ = srv.Stop() })

	client := newSIPClient(t, cfg.SIPListen)
	dm.Register(&platform.Device{ID: testDeviceID, NetAddr: client.conn.LocalAddr().String()})
	dev, _ := dm.Device(testDeviceID)
	dev.Status.Store(platform.DeviceOnline)

	// The periodic loop must send Catalog queries to the online device.
	require.Eventually(t, func() bool {
		for {
			req := client.nextRequest(100 * time.Millisecond)
			if req == nil {
				return false
			}
			if req.Method() == sip.MESSAGE && strings.Contains(string(req.Body()), "<CmdType>Catalog</CmdType>") {
				return true
			}
		}
	}, 5*time.Second, 50*time.Millisecond, "catalog loop must query the device")

	// Answer one query with a catalog: the channels register.
	catalog, err := manscdp.Encode(manscdp.Catalog{
		CmdType: manscdp.CmdCatalog, SN: 1, DeviceID: testDeviceID, SumNum: 1,
		Item: []manscdp.Item{{DeviceID: fakeChannelID, Name: "Front Door", Parental: 0}},
	})
	require.NoError(t, err)
	answer := buildRequest(t, sip.MESSAGE, testDeviceID, testServerID, cfg.SIPListen, client.localPort(), string(catalog))
	_, err = client.conn.WriteToUDP([]byte(answer.String()), client.addr)
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		return len(dm.Channels(testDeviceID)) == 1
	}, 5*time.Second, 50*time.Millisecond, "catalog answer must register the channel")
}

func TestStartFailedRollsBackForRetry(t *testing.T) {
	// Occupy the listen port so gosip's listener bind fails.
	occupied, err := net.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	defer occupied.Close()
	addr := occupied.LocalAddr().String()

	cfg := testConfig(t)
	cfg.SIPListen = addr

	pmm := platform.NewPortManager(30500, 30510)
	dm := platform.NewDeviceManager(60 * time.Second)
	sm := platform.NewSessionManager(pmm, cfg.ServerID)
	srv := NewServer(cfg, dm, sm, nil)

	require.Error(t, srv.Start(context.Background()), "Start on an occupied port must fail")

	// Idempotent Stop on the failed start.
	require.NoError(t, srv.Stop())

	// After the port is freed, a retry must succeed — proving startFailed
	// rolled the started state back instead of wedging it.
	require.NoError(t, occupied.Close())
	require.Eventually(t, func() bool {
		return srv.Start(context.Background()) == nil
	}, 5*time.Second, 100*time.Millisecond, "Start must be retryable after a failed startup")

	t.Cleanup(func() { _ = srv.Stop() })
}
