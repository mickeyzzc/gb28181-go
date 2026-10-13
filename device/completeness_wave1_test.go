package device

import (
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// Wave-1 device-side completeness (twin of gb28181-rs PR #86): PresetQuery
// response, negotiated registration expiry, the silence predicate, and the
// honest Subscription-State lifetime.

func TestDispatch_PresetQuery(t *testing.T) {
	msg := buildInboundMessage(`<Query><CmdType>PresetQuery</CmdType><SN>33</SN><DeviceID>34020000001320000001</DeviceID></Query>`)
	ok200, queued, err := DispatchInboundMessage(msg, testDeviceContext(), nil)
	if err != nil {
		t.Fatalf("DispatchInboundMessage failed: %v", err)
	}
	if ok200.StatusCode != 200 {
		t.Errorf("expected 200 OK, got status %d", ok200.StatusCode)
	}
	if len(queued) == 0 {
		t.Fatal("expected a queued response, got nil")
	}
	for _, w := range []string{
		`<Response CmdType="PresetQuery" SN="33">`,
		"<DeviceID>34020000001320000001</DeviceID>",
		"<SumNum>0</SumNum>",
		`<PresetList Num="0">`,
	} {
		if !strings.Contains(queued[0].Body, w) {
			t.Errorf("queued body missing %q:\n%s", w, queued[0].Body)
		}
	}
}

func TestBuildPresetListResponseMessage_Golden(t *testing.T) {
	msg := BuildPresetListResponseMessage("7", "34020000001320000001", []PresetItem{
		{PresetID: "1", PresetName: "正门"},
		{PresetID: "2", PresetName: "后院"},
	})
	for _, w := range []string{
		`<Response CmdType="PresetQuery" SN="7">`,
		"<SumNum>2</SumNum>",
		`<PresetList Num="2">`,
		"<Item><PresetID>1</PresetID><PresetName>正门</PresetName></Item>",
		"<Item><PresetID>2</PresetID><PresetName>后院</PresetName></Item>",
	} {
		if !strings.Contains(msg.Body, w) {
			t.Errorf("body missing %q:\n%s", w, msg.Body)
		}
	}
}

func TestNegotiatedExpiresSecs(t *testing.T) {
	cases := []struct {
		expires, contact string
		want             int64
	}{
		{"7200", "", 7200}, // header
		{"30", "", 0},      // below the 60s floor
		{"", "<sip:34020000002000000001@3402000000>;expires=1800", 1800}, // contact fallback
		{"600", "<sip:x>;expires=9999", 600},                             // header wins
		{"", "", 0},                                                      // absent
		{"not-a-number", "", 0},                                          // garbage
		{"", "<sip:x>;expires=45", 0},                                    // contact below floor
	}
	for _, tc := range cases {
		if got := negotiatedExpiresSecs(tc.expires, tc.contact); got != tc.want {
			t.Errorf("negotiatedExpiresSecs(%q,%q) = %d, want %d", tc.expires, tc.contact, got, tc.want)
		}
	}
}

func TestPlatformSilent(t *testing.T) {
	heartbeat := 60 * time.Second
	if platformSilent(179*time.Second, heartbeat, 3) {
		t.Error("179s of 3×60s must not count as silent")
	}
	if !platformSilent(180*time.Second, heartbeat, 3) {
		t.Error("180s of 3×60s must count as silent")
	}
	if !platformSilent(10*time.Minute, heartbeat, 3) {
		t.Error("10min of 3×60s must count as silent")
	}
	// Degenerate configs clamp instead of misfiring.
	if platformSilent(0, 0, 3) {
		t.Error("zero elapsed must never be silent")
	}
	if platformSilent(5*time.Second, heartbeat, 0) {
		t.Error("timeout count 0 must never trip")
	}
}

// TestDispatch_RecordInfoPagination: 25 matching segments page into 3
// MESSAGEs — every page carries the TOTAL SumNum (the platform
// accumulates by SN), RecordList Num the page's own count.
func TestDispatch_RecordInfoPagination(t *testing.T) {
	var segments []SegmentMeta
	base := time.Date(2026, 10, 13, 0, 0, 0, 0, time.Local).UnixMilli()
	for i := range 25 {
		segments = append(segments, SegmentMeta{
			File:    fmt.Sprintf("2026/10/13/%02d-00-00.h264", i),
			StartMS: base + int64(i)*60_000,
			EndMS:   base + int64(i)*60_000 + 30_000,
		})
	}
	idx := &fakeRecordingIndex{segments: segments}
	body := `<Query><CmdType>RecordInfo</CmdType><SN>77</SN><DeviceID>dev</DeviceID>` +
		`<StartTime>2026-10-13T00:00:00</StartTime><EndTime>2026-10-13T23:59:59</EndTime></Query>`
	ok200, pages, err := DispatchInboundMessage(buildInboundMessage(body), testDeviceContext(), idx)
	if err != nil {
		t.Fatalf("DispatchInboundMessage failed: %v", err)
	}
	if ok200.StatusCode != 200 {
		t.Errorf("expected 200 OK, got %d", ok200.StatusCode)
	}
	if len(pages) != 3 {
		t.Fatalf("expected 3 pages for 25 segments at page size 10, got %d", len(pages))
	}
	wantNums := []string{`Num="10"`, `Num="10"`, `Num="5"`}
	for i, page := range pages {
		if !strings.Contains(page.Body, "<SumNum>25</SumNum>") {
			t.Errorf("page %d: SumNum must carry the total:\n%s", i, page.Body)
		}
		if !strings.Contains(page.Body, "<RecordList "+wantNums[i]+">") {
			t.Errorf("page %d: RecordList %s missing:\n%s", i, wantNums[i], page.Body)
		}
		if !strings.Contains(page.Body, fmt.Sprintf("%02d-00-00.h264", i*10)) {
			t.Errorf("page %d: first item mismatch (expected segment %d):\n%s", i, i*10, page.Body)
		}
	}
}

// TestBuildCatalogResponse_NumAttr: DeviceList now carries the
// standard-required Num attribute (A.2.6.1), matching the Rust twin's
// wire form.
func TestBuildCatalogResponse_NumAttr(t *testing.T) {
	msg := BuildCatalogResponseMessage("5", "dev", nil)
	if !strings.Contains(msg.Body, `<DeviceList Num="0">`) {
		t.Errorf("empty catalog missing DeviceList Num:\n%s", msg.Body)
	}
}

// sendMediaEndTCPDialog: a TCP-dialog mediaEndNotify routes the §9.4.2
// INFO through the server's connection table (issue #60 follow-up).
func TestSendMediaEndTCPDialog(t *testing.T) {
	s := &Server{}
	addr := &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 15060}
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()
	s.tcpConns.Store(addr.String(), server)

	end := &mediaEndNotify{
		tcpAddr: addr,
		msg: SipMessage{
			Method:      "INFO",
			RequestURI:  "sip:34020000001320000001@3402000000",
			CallID:      "pb-1",
			CSeq:        "2 INFO",
			ContentType: "Application/MANSRTSP",
			Body:        "MediaStatus: Play Finished\r\n",
			Headers:     make(map[string]string),
		},
	}
	done := make(chan []byte, 1)
	go func() {
		buf := make([]byte, 4096)
		n, _ := client.Read(buf)
		done <- buf[:n]
	}()
	if err := s.sendMediaEnd(end); err != nil {
		t.Fatalf("sendMediaEnd: %v", err)
	}
	select {
	case got := <-done:
		text := string(got)
		if !strings.Contains(text, "INFO sip:") || !strings.Contains(text, "MediaStatus: Play Finished") {
			t.Fatalf("unexpected INFO on the dialog connection:\n%s", text)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the INFO on the connection")
	}
}

// TCP-dialog subscriptions book and their NOTIFYs ride the connection
// table's write half (the notifier's TCP hook).
func TestTCPSubscriptionNotifyRidesConnection(t *testing.T) {
	n := newDeviceNotifier()
	var mu sync.Mutex
	var got []byte
	n.deviceID.Store(ptrStr("34020000001320000001"))
	n.domain.Store(ptrStr("3402000000"))
	n.localIP.Store(ptrStr("192.168.62.104"))
	port := uint16(5060)
	n.localPort.Store(&port)

	tcpPeer := &net.TCPAddr{IP: net.ParseIP("192.168.63.30"), Port: 5060}
	ready := make(chan struct{}, 4)
	n.BindTCP(func(data []byte, addr *net.TCPAddr) error {
		if addr.String() != tcpPeer.String() {
			t.Errorf("NOTIFY routed to %s, want %s", addr, tcpPeer)
		}
		mu.Lock()
		got = append(got, data...)
		mu.Unlock()
		ready <- struct{}{}
		return nil
	})

	n.registry.upsert(EventAlarm, 120, tcpPeer,
		"<sip:34020000002000000001@3402000000>", "<sip:34020000001320000001@3402000000>", "sub-tcp", 1)
	if !n.Subscribed(EventAlarm) {
		t.Fatal("TCP subscription must book")
	}
	if !n.SendAlarm("2", "5", "2026-10-13T12:00:00", "1", "motion") {
		t.Fatal("SendAlarm failed with a live TCP subscription")
	}
	select {
	case <-ready:
	case <-time.After(2 * time.Second):
		t.Fatal("NOTIFY never reached the TCP hook")
	}
	if !strings.Contains(string(got), "Event: Alarm") &&
		!strings.Contains(string(got), "NOTIFY") {
		t.Fatalf("unexpected NOTIFY bytes:\n%s", string(got))
	}
}

func ptrStr(v string) *string { return &v }
