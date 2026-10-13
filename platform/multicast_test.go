package platform

import (
	"strings"
	"testing"
)

// Multicast media (issue #111 follow-up): a group address as the
// INVITE's serverIP makes the session manager join the IGMP group
// (ListenMulticastUDP) and answer with the group in the c= line — one
// stream reaches every joined viewer.
func TestMulticastSessionJoinsGroup(t *testing.T) {
	sm := NewSessionManager(NewPortManager(42000, 43000), "34020000002000000001")
	ch := &Channel{ID: "34020000001320000001", DeviceID: "34020000001320000001"}

	const group = "239.255.42.42"
	answer, err := sm.Invite(ch, group, "127.0.0.1:5060", nil, nil, nil)
	if err != nil {
		t.Fatalf("Invite with multicast serverIP: %v", err)
	}
	defer sm.StopAll()
	if !strings.Contains(string(answer), "c=IN IP4 "+group) {
		t.Fatalf("answer must carry the group address:\n%s", answer)
	}
	if sm.SessionCount() != 1 {
		t.Fatalf("expected the multicast session registered, count=%d", sm.SessionCount())
	}
}
