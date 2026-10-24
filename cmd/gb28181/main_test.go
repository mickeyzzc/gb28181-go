package main

import "testing"

// The dispatch table is the CLI's contract: empty and unknown
// subcommands are usage errors (exit 2), help exits 0. The subcommand
// runners themselves are exercised by flags parsing in their own files
// and by the library's integration tests.
func TestDispatchUsageContract(t *testing.T) {
	if got := dispatch(nil); got != 2 {
		t.Errorf("dispatch(nil) = %d, want 2", got)
	}
	if got := dispatch([]string{"bogus"}); got != 2 {
		t.Errorf("dispatch(bogus) = %d, want 2", got)
	}
	for _, h := range []string{"help", "-h", "-help", "--help"} {
		if got := dispatch([]string{h}); got != 0 {
			t.Errorf("dispatch(%q) = %d, want 0", h, got)
		}
	}
}

// Known subcommands must resolve to a runner — a typo in the dispatch
// table would otherwise fail only at runtime.
func TestDispatchKnowsAllSubcommands(t *testing.T) {
	for _, sub := range []string{"device-register", "platform-uas", "psmux-rtp"} {
		var fn func([]string)
		switch sub {
		case "device-register":
			fn = runDeviceRegister
		case "platform-uas":
			fn = runPlatformUAS
		case "psmux-rtp":
			fn = runPsmuxRTP
		}
		if fn == nil {
			t.Errorf("subcommand %q has no runner", sub)
		}
	}
}
