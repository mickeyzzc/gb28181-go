package device

// GB/T 28181-2022 §9.12.1 voice broadcast, device half (issue #84):
// after the Broadcast notification (信令1, A.2.5.5) the device
// acknowledges (信令3, A.2.6.11) and — with an audio sink installed —
// INVITEs the announced source (信令5, s=Play / m=audio / Subject
// header) and receives the platform's G.711 RTP through the same
// talkback receive machinery (runTalkbackReceiver). The outgoing INVITE
// is single-shot with a 5s answer window; the session occupies the
// shared media slot, so BYE / recycle / stop tear it down with the
// existing paths.

import (
	"context"
	"encoding/xml"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/mickeyzzc/gb28181-go/manscdp"
)

// decodeBroadcast recognizes a Broadcast notification body.
func decodeBroadcast(body string) (manscdp.Broadcast, bool) {
	ct, v, err := manscdp.Decode([]byte(body))
	if err != nil || ct != manscdp.CmdBroadcast {
		return manscdp.Broadcast{}, false
	}
	b, ok := v.(manscdp.Broadcast)
	return b, ok
}

// BuildBroadcastResponseMessage assembles the 信令3 acknowledgement
// MESSAGE (A.2.6.11) toward the platform.
func BuildBroadcastResponseMessage(sn int, deviceID string, ok bool) SipMessage {
	resp := manscdp.BuildBroadcastResponse(sn, deviceID, ok)
	xmlData, err := xml.Marshal(resp)
	if err != nil {
		slog.Error("gb28181: failed to marshal Broadcast response", "error", err)
		return SipMessage{}
	}
	return SipMessage{
		Method:      "MESSAGE",
		ContentType: "Application/MANSCDP+xml",
		Body:        string(xmlData),
		UserAgent:   UserAgent,
		Headers:     make(map[string]string),
	}
}

// followBroadcast runs the §9.12.1 device half after the notification's
// 200 OK: send the acknowledgement (OK when audio-capable), then start
// the receive session toward the announced source. Runs on its own
// goroutine — the 5s INVITE answer window must not stall signaling.
func (s *Server) followBroadcast(ctx context.Context, notify manscdp.Broadcast, fromAddr net.Addr) {
	s.mu.Lock()
	sink := s.audioSink
	s.mu.Unlock()

	platformAddr := &net.UDPAddr{
		IP:   net.ParseIP(s.cfg.PlatformSIPAddress),
		Port: s.cfg.PlatformSIPPort,
	}
	ack := BuildBroadcastResponseMessage(notify.SN, s.cfg.DeviceID, sink != nil)
	ack.RequestURI = fmt.Sprintf("sip:%s@%s", s.cfg.SIPDomain, s.cfg.SIPDomain)
	ack.From = fmt.Sprintf("<sip:%s@%s>", s.cfg.DeviceID, s.cfg.SIPDomain)
	ack.To = fmt.Sprintf("<sip:%s@%s>", s.cfg.SIPDomain, s.cfg.SIPDomain)
	ack.CallID = fmt.Sprintf("bresp%d@%s", time.Now().UnixNano(), s.cfg.DeviceID)
	ack.CSeq = "1 MESSAGE"
	ack.Via = fmt.Sprintf("SIP/2.0/UDP %s:%d;branch=z9hG4bKbc%016x", s.cfg.PlatformSIPAddress, s.cfg.LocalSIPPort, time.Now().UnixNano())
	ack.Contact = fmt.Sprintf("<sip:%s@%s:%d>", s.cfg.DeviceID, s.cfg.PlatformSIPAddress, s.cfg.LocalSIPPort)
	if err := s.sendSIP(ack.Serialize(), platformAddr); err != nil {
		slog.Warn("gb28181: failed to send Broadcast response", "error", err)
	}

	if sink == nil {
		slog.Info("gb28181: broadcast declined — no audio sink installed")
		return
	}
	s.startBroadcastSession(ctx, notify, sink, platformAddr)
}

// broadcastInvite builds the 信令5 audio-only INVITE toward the
// announced source: s=Play (live), m=audio with the device's receive
// port, y= SSRC, and the GB28181 Subject convention
// "<sourceID>:<ssrc>,<deviceID>:0".
func broadcastInvite(cfg Config, notify manscdp.Broadcast, platformAddr *net.UDPAddr, localIP string, mediaPort int, ssrc uint32) SipMessage {
	// Request-URI targets the platform's actual SIP address (the domain
	// is not DNS-routable); the user part is the announced source.
	uri := fmt.Sprintf("sip:%s@%s:%d", notify.SourceID, platformAddr.IP, platformAddr.Port)
	via := fmt.Sprintf("SIP/2.0/UDP %s:%d;branch=z9hG4bKbi%016x", localIP, cfg.LocalSIPPort, randUint32())
	sdp := fmt.Sprintf("v=0\r\no=- 0 0 IN IP4 %s\r\ns=Play\r\nc=IN IP4 %s\r\nt=0 0\r\n"+
		"m=audio %d RTP/AVP 8\r\na=rtpmap:8 PCMA/8000\r\ny=%d\r\n",
		localIP, localIP, mediaPort, ssrc)
	return SipMessage{
		Method:      "INVITE",
		RequestURI:  uri,
		From:        fmt.Sprintf("<sip:%s@%s>;tag=%08x", cfg.DeviceID, cfg.SIPDomain, randUint32()),
		To:          fmt.Sprintf("<sip:%s@%s>", notify.SourceID, cfg.SIPDomain),
		CallID:      fmt.Sprintf("bcast%d@%s", time.Now().UnixNano(), localIP),
		CSeq:        "1 INVITE",
		Contact:     fmt.Sprintf("<sip:%s@%s:%d>", cfg.DeviceID, localIP, cfg.LocalSIPPort),
		Via:         via,
		MaxForwards: "70",
		ContentType: "application/sdp",
		Body:        sdp,
		UserAgent:   UserAgent,
		Headers: map[string]string{
			"Subject": fmt.Sprintf("%s:%d,%s:0", notify.SourceID, ssrc, cfg.DeviceID),
		},
	}
}

// broadcastAck builds the in-dialog ACK for the platform's 200 OK (the
// To header — including its tag — comes verbatim from the response).
func broadcastAck(inv SipMessage, resp SipMessage) SipMessage {
	cseqNum := parseCSeqNumber(inv.CSeq)
	return SipMessage{
		Method:      "ACK",
		RequestURI:  inv.RequestURI,
		From:        inv.From,
		To:          resp.To,
		CallID:      inv.CallID,
		CSeq:        cseqNum + " ACK",
		Via:         fmt.Sprintf("SIP/2.0/UDP %s;branch=z9hG4bKba%016x", viaHost(inv.Via), randUint32()),
		MaxForwards: "70",
		UserAgent:   UserAgent,
		Headers:     make(map[string]string),
	}
}

// viaHost extracts "host:port" from a Via header value.
func viaHost(via string) string {
	rest := strings.TrimPrefix(via, "SIP/2.0/UDP")
	if i := strings.Index(rest, ";"); i >= 0 {
		rest = rest[:i]
	}
	return strings.TrimSpace(rest)
}

// startBroadcastSession sends the 信令5 INVITE, completes the handshake
// on the platform's 200 OK, and receives G.711 RTP into the sink on the
// shared media slot.
func (s *Server) startBroadcastSession(ctx context.Context, notify manscdp.Broadcast, sink TalkbackSink, platformAddr *net.UDPAddr) {
	s.mu.Lock()
	s.teardownMediaLocked()
	s.mu.Unlock()

	mediaConn, err := net.ListenUDP("udp", nil)
	if err != nil {
		slog.Warn("gb28181: failed to bind broadcast media UDP", "error", err)
		return
	}
	mediaPort := mediaConn.LocalAddr().(*net.UDPAddr).Port

	localIPAddr, err := getLocalIP(ctx, platformAddr.String())
	if err != nil {
		localIPAddr = localIP()
	}

	ssrc := randUint32()
	inv := broadcastInvite(s.cfg, notify, platformAddr, localIPAddr, int(mediaPort), ssrc)
	slog.Info("gb28181: broadcast INVITE to source", "source", notify.SourceID, "ssrc", ssrc)
	if err := s.sendSIP(inv.Serialize(), platformAddr); err != nil {
		slog.Warn("gb28181: failed to send broadcast INVITE", "error", err)
		_ = mediaConn.Close()
		return
	}

	select {
	case resp := <-s.inviteRespCh:
		if resp.StatusCode != 200 {
			slog.Warn("gb28181: broadcast INVITE rejected", "status", resp.StatusCode)
			_ = mediaConn.Close()
			return
		}
		ackMsg := broadcastAck(inv, resp)
		if err := s.sendSIP(ackMsg.Serialize(), platformAddr); err != nil {
			slog.Warn("gb28181: failed to send broadcast ACK", "error", err)
		}
		_, mediaCancel := context.WithCancel(ctx)
		s.mu.Lock()
		s.mediaConn = mediaConn
		s.mediaCancel = mediaCancel
		s.mu.Unlock()
		slog.Info("gb28181: broadcast session receiving", "call_id", inv.CallID, "port", mediaPort)
		go func() {
			defer mediaCancel()
			runTalkbackReceiver(mediaConn, sink, ssrc, AudioPCMA)
		}()
	case <-time.After(5 * time.Second):
		slog.Warn("gb28181: broadcast INVITE unanswered — session abandoned")
		_ = mediaConn.Close()
	case <-ctx.Done():
		_ = mediaConn.Close()
	}
}

// parseCSeqNumber extracts the numeric CSeq for reuse in ACKs.
func parseCSeqNumber(cseq string) string {
	if parts := strings.Fields(cseq); len(parts) > 0 {
		if _, err := strconv.Atoi(parts[0]); err == nil {
			return parts[0]
		}
	}
	return "1"
}
