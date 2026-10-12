package device_test

import (
	"strconv"
	"strings"
	"testing"
)

// Wave-1 twin: the NOTIFY Subscription-State must advertise the
// subscription's real remaining lifetime (was a hard-coded 3600).
func TestSubscriptionStateCarriesRemainingLifetime(t *testing.T) {
	srv, platConn, devAddr := startSubTestServer(t)
	writeSnapMsg(t, platConn, subscribeMessage("sub-rem", "Alarm", 120), devAddr)
	readNotify(t, platConn) // 200 OK

	if !srv.Notifier().SendAlarm("2", "5", "2026-09-15T12:00:00", "1", "motion") {
		t.Fatal("SendAlarm failed with a live subscription")
	}
	notify := readNotify(t, platConn)
	state := notify.Headers["Subscription-State"]
	if !strings.HasPrefix(state, "active;expires=") {
		t.Fatalf("Subscription-State = %q", state)
	}
	secs, err := strconv.ParseInt(strings.TrimPrefix(state, "active;expires="), 10, 64)
	if err != nil {
		t.Fatalf("expires value not numeric: %q", state)
	}
	if secs <= 0 || secs > 120 {
		t.Fatalf("advertised expires = %d, want 1..120 (was a hard-coded 3600)", secs)
	}
}
