package device

// The SIP User-Agent is host-brandable: the default is neutral, and hosts
// can rebrand by assigning device.UserAgent before building the server
// (#28 — the library must not carry a specific vendor's brand).

import (
	"strings"
	"testing"
)

func TestUserAgentNeutralDefault(t *testing.T) {
	if strings.Contains(UserAgent, "MiBee") {
		t.Fatalf("default UserAgent carries a vendor brand: %q", UserAgent)
	}

	msg := BuildCatalogResponseMessage("1", "34020000001320000001", []ChannelItem{{DeviceID: "ch"}})
	if msg.UserAgent != UserAgent {
		t.Errorf("builder UserAgent = %q, want package default %q", msg.UserAgent, UserAgent)
	}
}

func TestUserAgentHostOverride(t *testing.T) {
	orig := UserAgent
	defer func() { UserAgent = orig }()

	UserAgent = "Acme-Cam/2.1"

	info := BuildDeviceInfoResponseMessage("2", "34020000001320000001", DeviceItem{DeviceID: "34020000001320000001"})
	if info.UserAgent != "Acme-Cam/2.1" {
		t.Errorf("host override not honored: %q", info.UserAgent)
	}

	ok200 := Build200OK(buildInvite("call-1", "a", "b", ""), "", "")
	if ok200.UserAgent != "Acme-Cam/2.1" {
		t.Errorf("response override not honored: %q", ok200.UserAgent)
	}
}
