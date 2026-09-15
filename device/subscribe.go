package device

// SUBSCRIBE/NOTIFY framework, device side (GB/T 28181-2016 §9.5 / 2022,
// issue #80's subscription half; twin of gb28181-rs #66).
//
// The platform SUBSCRIBEs to Catalog / Alarm / MobilePosition; the device
// answers 200 OK echoing Expires, keeps per-event bookkeeping with
// renewal/expiry, and sends SIP NOTIFY requests on the subscription
// dialog when something changes. Hosts hold the DeviceNotifier (from
// Server.Notifier()) and call SendAlarm / SendCatalogChange /
// SendMobilePosition whenever the business side has something to report
// — no-ops until the platform subscribes. Wire shapes mirror the Rust
// twin's goldens (gb28181-rs #66), which in turn mirror this repo's
// platform-side parsing (platform/sip subscribe.go / handleNotify).

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mickeyzzc/gb28181-go/manscdp"
)

// notifyBranch returns a fresh Via branch for NOTIFY requests (RFC 3261
// §8.1.1.7 magic-cookie prefix; crypto/rand — branches must not be
// predictable).
func notifyBranch() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("z9hG4bK%d", time.Now().UnixNano())
	}
	return "z9hG4bK" + hex.EncodeToString(b[:])
}

// SubscribeEvent is a subscription subject (SIP Event header value).
type SubscribeEvent string

const (
	EventCatalog        SubscribeEvent = "Catalog"
	EventAlarm          SubscribeEvent = "Alarm"
	EventMobilePosition SubscribeEvent = "MobilePosition"
)

// ParseSubscribeEvent parses an Event header value (or the SUBSCRIBE
// body CmdType); false for unknown subjects — callers keep answering
// 200 OK without bookkeeping (legacy-platform safe).
func ParseSubscribeEvent(v string) (SubscribeEvent, bool) {
	switch strings.TrimSpace(v) {
	case "Catalog":
		return EventCatalog, true
	case "Alarm":
		return EventAlarm, true
	case "MobilePosition":
		return EventMobilePosition, true
	}
	return "", false
}

// PositionReport is one mobile-position report; fields are wire-verbatim
// strings (fixed cameras report configured constants).
type PositionReport struct {
	Time      string
	Longitude string
	Latitude  string
	Speed     string
	Direction string
	Altitude  string
}

// PositionSource is the host-provided periodic position source. Pulled
// on the report cadence; nil skips that report.
type PositionSource interface {
	CurrentPosition() *PositionReport
}

type subscription struct {
	expiresAt time.Time
	peer      *net.UDPAddr
	// The SUBSCRIBE's From (platform) — becomes the NOTIFY's To.
	from string
	// The SUBSCRIBE's To (this device) — becomes the NOTIFY's From.
	to       string
	callID   string
	nextCSeq uint32
}

// subscriptionRegistry is per-event bookkeeping. One subscription per
// event (the standard model for a single-platform camera); a renewed
// SUBSCRIBE refreshes the deadline and dialog snapshot.
type subscriptionRegistry struct {
	mu   sync.Mutex
	subs map[SubscribeEvent]*subscription
	sn   atomic.Uint32
}

func newSubscriptionRegistry() *subscriptionRegistry {
	return &subscriptionRegistry{subs: make(map[SubscribeEvent]*subscription)}
}

func (r *subscriptionRegistry) upsert(event SubscribeEvent, expiresSecs int64, peer *net.UDPAddr, from, to, callID string, cseqBase uint32) {
	if expiresSecs < 1 {
		expiresSecs = 1
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.subs[event] = &subscription{
		expiresAt: time.Now().Add(time.Duration(expiresSecs) * time.Second),
		peer:      peer,
		from:      from,
		to:        to,
		callID:    callID,
		nextCSeq:  cseqBase + 1,
	}
}

// active returns the live subscription (advancing its CSeq); expired
// entries are dropped on read.
func (r *subscriptionRegistry) active(event SubscribeEvent) (*net.UDPAddr, string, string, string, uint32, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	sub, ok := r.subs[event]
	if !ok {
		return nil, "", "", "", 0, false
	}
	if !time.Now().Before(sub.expiresAt) {
		delete(r.subs, event)
		return nil, "", "", "", 0, false
	}
	cseq := sub.nextCSeq
	sub.nextCSeq++
	return sub.peer, sub.from, sub.to, sub.callID, cseq, true
}

func (r *subscriptionRegistry) isActive(event SubscribeEvent) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	sub, ok := r.subs[event]
	if !ok {
		return false
	}
	if !time.Now().Before(sub.expiresAt) {
		delete(r.subs, event)
		return false
	}
	return true
}

func (r *subscriptionRegistry) nextSN() int {
	return int(r.sn.Add(1))
}

// DeviceNotifier sends NOTIFYs for subscribed events. No-op (returns
// false) when the event has no live subscription — hosts never need to
// check first. Obtain from Server.Notifier().
type DeviceNotifier struct {
	registry *subscriptionRegistry

	conn      atomic.Pointer[net.UDPConn]
	deviceID  atomic.Pointer[string]
	domain    atomic.Pointer[string]
	localIP   atomic.Pointer[string]
	localPort atomic.Pointer[uint16]
}

func newDeviceNotifier() *DeviceNotifier {
	return &DeviceNotifier{registry: newSubscriptionRegistry()}
}

// bind attaches the sending context; called from the UDP listener setup
// before the server starts answering SUBSCRIBEs.
func (n *DeviceNotifier) bind(conn *net.UDPConn, deviceID, domain, localIP string, localPort uint16) {
	n.conn.Store(conn)
	n.deviceID.Store(&deviceID)
	n.domain.Store(&domain)
	n.localIP.Store(&localIP)
	n.localPort.Store(&localPort)
}

// Subscribed reports whether the platform currently holds a live
// subscription to the event (handy for UI/metrics; the senders already
// no-op safely).
func (n *DeviceNotifier) Subscribed(event SubscribeEvent) bool {
	return n.registry.isActive(event)
}

// SendAlarm sends an alarm NOTIFY (§9.5.2). priority "1"-"4" (severity),
// method per A.2.6.1 (e.g. "5" motion), time as a GB28181 timestamp;
// alarmType (the 2022 classification code) may be "".
func (n *DeviceNotifier) SendAlarm(priority, method, alarmTime, alarmType, description string) bool {
	body, err := manscdp.Encode(manscdp.Alarm{
		CmdType:          manscdp.CmdAlarm,
		SN:               n.registry.nextSN(),
		DeviceID:         n.ptr(&n.deviceID),
		AlarmPriority:    priority,
		AlarmMethod:      method,
		AlarmTime:        alarmTime,
		AlarmDescription: description,
		AlarmType:        alarmType,
	})
	if err != nil {
		return false
	}
	return n.sendNotify(EventAlarm, string(body))
}

// SendMobilePosition sends a mobile-position NOTIFY (§9.5.3).
func (n *DeviceNotifier) SendMobilePosition(p *PositionReport) bool {
	body, err := manscdp.Encode(manscdp.MobilePosition{
		CmdType:   manscdp.CmdMobilePosition,
		SN:        n.registry.nextSN(),
		DeviceID:  n.ptr(&n.deviceID),
		Time:      p.Time,
		Longitude: p.Longitude,
		Latitude:  p.Latitude,
		Speed:     p.Speed,
		Direction: p.Direction,
		Altitude:  p.Altitude,
	})
	if err != nil {
		return false
	}
	return n.sendNotify(EventMobilePosition, string(body))
}

// SendCatalogChange sends a catalog-change NOTIFY (§9.5.1) with the
// changed channels.
func (n *DeviceNotifier) SendCatalogChange(items []manscdp.Item) bool {
	body, err := manscdp.Encode(manscdp.CatalogNotify{
		CmdType:  manscdp.CmdCatalog,
		SN:       n.registry.nextSN(),
		DeviceID: n.ptr(&n.deviceID),
		SumNum:   len(items),
		Item:     items,
	})
	if err != nil {
		return false
	}
	return n.sendNotify(EventCatalog, string(body))
}

func (n *DeviceNotifier) ptr(p *atomic.Pointer[string]) string {
	if v := p.Load(); v != nil {
		return *v
	}
	return ""
}

// sendNotify sends one NOTIFY on the subscription dialog. Direction
// mirrors the SUBSCRIBE: our From is the device, To is the subscriber.
func (n *DeviceNotifier) sendNotify(event SubscribeEvent, body string) bool {
	peer, subFrom, subTo, callID, cseq, ok := n.registry.active(event)
	if !ok {
		return false
	}
	conn := n.conn.Load()
	deviceID, domain, localIP := n.ptr(&n.deviceID), n.ptr(&n.domain), n.ptr(&n.localIP)
	localPort := n.localPort.Load()
	if conn == nil || deviceID == "" || domain == "" || localIP == "" || localPort == nil {
		return false
	}

	msg := SipMessage{
		Method:     "NOTIFY",
		RequestURI: fmt.Sprintf("sip:%s@%s", deviceID, domain),
		// NOTIFY direction: From = the device (the SUBSCRIBE's To), To =
		// the platform subscriber (the SUBSCRIBE's From).
		From:        subTo,
		To:          subFrom,
		CallID:      callID,
		CSeq:        fmt.Sprintf("%d NOTIFY", cseq),
		MaxForwards: "70",
		Via:         fmt.Sprintf("SIP/2.0/UDP %s:%d;rport;branch=%s", localIP, *localPort, notifyBranch()),
		ContentType: "Application/MANSCDP+xml",
		Body:        body,
		UserAgent:   UserAgent,
		Headers: map[string]string{
			"Event":              string(event),
			"Subscription-State": "active;expires=3600",
		},
	}
	if _, err := conn.WriteToUDP(msg.Serialize(), peer); err != nil {
		slog.Warn("gb28181: NOTIFY send failed", "event", event, "error", err)
		return false
	}
	return true
}

// parseSubscribeInterval extracts the MobilePosition report cadence
// (<Interval> seconds) from a SUBSCRIBE body; 0 when absent/unparseable
// (callers default to 5s, matching the Go platform's request cadence).
func parseSubscribeInterval(body string) int {
	const tag = "<Interval>"
	start := strings.Index(body, tag)
	if start < 0 {
		return 0
	}
	start += len(tag)
	end := strings.Index(body[start:], "</Interval>")
	if end < 0 {
		return 0
	}
	if v, err := strconv.Atoi(strings.TrimSpace(body[start : start+end])); err == nil {
		return v
	}
	return 0
}

// handleSubscribe answers an inbound SUBSCRIBE (issue #80): supported
// subjects are booked with their dialog snapshot and refreshed on
// re-SUBSCRIBE; every subject answers 200 OK with the request's Expires
// echoed (unknown subjects stay answered-but-unbooked — legacy-platform
// safe). A MobilePosition subscription with an installed position
// source also starts (or re-cadences) the periodic report loop on the
// SUBSCRIBE's Interval (default 5s).
func (s *Server) handleSubscribe(msg SipMessage, addr net.Addr) {
	eventStr := msg.Headers["Event"]
	if eventStr == "" {
		// Some platforms only carry the subject in the body's CmdType
		// (the SUBSCRIBE body mirrors the MANSCDP form).
		if ct, v, err := manscdp.Decode([]byte(msg.Body)); err == nil {
			if sub, ok := v.(manscdp.Subscribe); ok {
				eventStr = string(ct)
				_ = sub
			}
		}
	}
	expires := int64(3600)
	if v, err := strconv.ParseInt(strings.TrimSpace(msg.Expires), 10, 64); err == nil && v > 0 {
		expires = v
	}

	if event, ok := ParseSubscribeEvent(eventStr); ok {
		if udpPeer, okPeer := addr.(*net.UDPAddr); okPeer {
			cseqBase := uint32(1)
			if fields := strings.Fields(msg.CSeq); len(fields) > 0 {
				if n, err := strconv.ParseUint(fields[0], 10, 32); err == nil {
					cseqBase = uint32(n)
				}
			}
			s.notifier.registry.upsert(event, expires, udpPeer, msg.From, msg.To, msg.CallID, cseqBase)
			slog.Info("gb28181: SUBSCRIBE booked", "event", eventStr, "expires", expires, "from", addr.String())

			if event == EventMobilePosition {
				s.mu.Lock()
				hasSource := s.positionSource != nil
				s.mu.Unlock()
				if hasSource {
					interval := parseSubscribeInterval(msg.Body)
					if interval <= 0 {
						interval = 5
					}
					s.restartPositionLoop(interval)
				}
			}
		}
	} else {
		slog.Warn("gb28181: SUBSCRIBE with unsupported Event — answered, not booked", "event", eventStr)
	}

	ok200 := Build200OK(msg, "", "")
	ok200.Expires = strings.TrimSpace(msg.Expires)
	if udpAddr, ok := addr.(*net.UDPAddr); ok {
		if _, err := s.sipConn.WriteToUDP(ok200.Serialize(), udpAddr); err != nil {
			slog.Warn("gb28181: failed to send SUBSCRIBE 200 OK", "error", err)
		}
	}
}

// restartPositionLoop re-cadences the periodic MobilePosition reports:
// the previous loop (if any) is cancelled and a fresh one starts.
func (s *Server) restartPositionLoop(intervalSecs int) {
	s.mu.Lock()
	old := s.positionCancel
	cancel := make(chan struct{})
	s.positionCancel = cancel
	source := s.positionSource
	notifier := s.notifier
	s.mu.Unlock()
	close(old)
	go func() {
		ticker := time.NewTicker(time.Duration(intervalSecs) * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if !notifier.Subscribed(EventMobilePosition) {
					continue
				}
				if p := source.CurrentPosition(); p != nil {
					notifier.SendMobilePosition(p)
				}
			case <-cancel:
				return
			}
		}
	}()
}
