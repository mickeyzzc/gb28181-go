package sip

// GB/T 28181-2022 §9.12.1 voice broadcast delivery (issue #84): the
// platform announces the broadcast (信令1, A.2.5.5 Broadcast Notify);
// the audio-capable device acknowledges (A.2.6.11) and INVITEs back
// (信令5). This server answers that INVITE with its audio source
// address (信令13/14) and streams host-provided G.711 RTP to the
// device's offered address — the UAS mirror of talk.go's UAC intercom,
// sharing its SDP/BYE conventions.

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync/atomic"
	"time"

	"github.com/ghettovoice/gosip/sip"
	"github.com/mickeyzzc/gb28181-go/manscdp"
)

// broadcastSession is one armed voice broadcast: the host's audio feed
// plus the dialog state captured when the device INVITEs back.
type broadcastSession struct {
	sourceID   string
	audio      <-chan []byte
	inviteReq  sip.Request
	inviteResp sip.Response
	cancel     context.CancelFunc
	ready      chan struct{}
}

// BroadcastHandle exposes the asynchronous voice-broadcast session.
type BroadcastHandle struct {
	s        *Server
	deviceID string
}

// Ready closes when the device's INVITE session is up — audio pushed
// into the source channel flows from that point.
func (h *BroadcastHandle) Ready() <-chan struct{} {
	h.s.mu.Lock()
	bs := h.s.broadcasts[h.deviceID]
	h.s.mu.Unlock()
	if bs == nil {
		ch := make(chan struct{})
		return ch
	}
	return bs.ready
}

// bcastSN numbers the Broadcast notification MESSAGEs.
var bcastSN atomic.Int64

// StartBroadcast announces a voice broadcast to deviceID on behalf of
// sourceID (信令1) and arms the delivery: when the device INVITEs back
// (§9.12.1 信令5), the server answers with its audio source address and
// streams the frames popped from audio (one per 20 ms tick, ~160-byte
// G.711 frames recommended) as RTP/PCMA. The announcement is
// fire-and-forget; use the handle's Ready channel to observe session
// establishment.
func (s *Server) StartBroadcast(deviceID, sourceID string, audio <-chan []byte) (*BroadcastHandle, error) {
	sn := int(bcastSN.Add(1))
	body := fmt.Sprintf(`<?xml version="1.0" encoding="GB2312"?>`+"\r\n"+
		`<Notify><CmdType>Broadcast</CmdType><SN>%d</SN><SourceID>%s</SourceID><TargetID>%s</TargetID></Notify>`+"\r\n",
		sn, sourceID, deviceID)
	if err := s.SendMessage(deviceID, []byte(body)); err != nil {
		return nil, fmt.Errorf("gb28181: send Broadcast notify: %w", err)
	}
	s.mu.Lock()
	s.broadcasts[deviceID] = &broadcastSession{
		sourceID: sourceID,
		audio:    audio,
		ready:    make(chan struct{}),
	}
	s.mu.Unlock()
	return &BroadcastHandle{s: s, deviceID: deviceID}, nil
}

// StopBroadcast tears the broadcast down: stops the audio sender and
// sends the in-dialog BYE (§9.12.1 信令17-20 simplified).
func (s *Server) StopBroadcast(deviceID string) error {
	s.mu.Lock()
	bs := s.broadcasts[deviceID]
	delete(s.broadcasts, deviceID)
	s.mu.Unlock()
	if bs == nil {
		return fmt.Errorf("gb28181: no broadcast armed for %q", deviceID)
	}
	if bs.cancel != nil {
		bs.cancel()
	}
	if bs.inviteReq != nil {
		return s.sendByeForTalk(bs.inviteReq, bs.inviteResp)
	}
	return nil
}

// answerBroadcastInvite serves the device's 信令5 audio-only INVITE when
// a broadcast is armed for it: 200 OK describing the platform's audio
// source socket, then the host-audio RTP sender toward the offered
// address. Returns false when the INVITE is not broadcast traffic (no
// armed session or not audio-only), leaving the caller's usual path.
func (s *Server) answerBroadcastInvite(req sip.Request, tx sip.ServerTransaction, deviceID, channelID string) bool {
	body := req.Body()
	if !isAudioOnlyOffer(body) {
		return false
	}
	s.mu.Lock()
	bs := s.broadcasts[deviceID]
	if bs == nil {
		bs = s.broadcasts[channelID]
	}
	s.mu.Unlock()
	if bs == nil {
		return false
	}
	ip, port, ok := sdpAudioAddress([]byte(body))
	if !ok {
		slog.Warn("gb28181: broadcast INVITE SDP carries no usable audio address", "device", deviceID)
		s.respond(req, tx, sip.StatusCode(488), "Not Acceptable Here", nil)
		return true
	}

	s.mu.Lock()
	srv := s.gosipSrv
	s.mu.Unlock()
	if srv == nil {
		return false
	}

	// The platform's audio source socket: its port is described in the
	// answer; RTP leaves through it toward the device's offered address.
	conn, err := net.ListenUDP("udp", nil)
	if err != nil {
		slog.Warn("gb28181: failed to bind broadcast audio UDP", "error", err)
		s.respond(req, tx, sip.StatusCode(488), "Not Acceptable Here", nil)
		return true
	}
	serverHost := s.localIPFor(req.Source())
	srcPort := conn.LocalAddr().(*net.UDPAddr).Port
	ssrc := randUint32()
	sdp := fmt.Sprintf("v=0\r\no=- 0 0 IN IP4 %s\r\ns=Play\r\nc=IN IP4 %s\r\nt=0 0\r\n"+
		"m=audio %d RTP/AVP 8\r\na=rtpmap:8 PCMA/8000\r\ny=%d\r\n",
		serverHost, serverHost, srcPort, ssrc)
	resp := sip.NewResponseFromRequest("", req, sip.StatusCode(200), "OK", sdp)
	ct := sip.ContentType("application/sdp")
	resp.AppendHeader(&ct)
	if _, err := srv.Respond(resp); err != nil {
		slog.Warn("gb28181: failed to answer broadcast INVITE", "error", err)
		_ = conn.Close()
		return true
	}

	dst := &net.UDPAddr{IP: net.ParseIP(ip), Port: int(port)}
	ctx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	bs.inviteReq = req
	bs.inviteResp = resp
	bs.cancel = cancel
	s.mu.Unlock()
	go runBroadcastSender(ctx, conn, dst, bs.audio)
	close(bs.ready)
	slog.Info("gb28181: broadcast session up", "device", deviceID, "dest", dst.String())
	return true
}

// runBroadcastSender paces the platform→device audio: one source frame
// per 20 ms tick packetized as RTP/PCMA (8 kHz clock — the timestamp
// advances by the payload length). Twin of the device package's
// runTalkbackSender (gb28181-go#94).
func runBroadcastSender(ctx context.Context, conn *net.UDPConn, dst *net.UDPAddr, frames <-chan []byte) {
	buf := make([]byte, 12+2048)
	ssrc := randUint32()
	seq := uint16(randUint32())
	ts := randUint32()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			_ = conn.Close()
			return
		case <-ticker.C:
			select {
			case frame := <-frames:
				if len(frame) == 0 || len(frame) > 2048 {
					continue
				}
				buf[0] = 0x80
				buf[1] = 8 // PCMA
				binary.BigEndian.PutUint16(buf[2:], seq)
				binary.BigEndian.PutUint32(buf[4:], ts)
				binary.BigEndian.PutUint32(buf[8:], ssrc)
				copy(buf[12:], frame)
				if _, err := conn.WriteToUDP(buf[:12+len(frame)], dst); err != nil {
					slog.Debug("gb28181: broadcast send loop ended", "error", err)
					_ = conn.Close()
					return
				}
				seq++
				ts += uint32(len(frame))
			default:
			}
		}
	}
}

// isAudioOnlyOffer reports whether an SDP body is an audio-only offer
// (m=audio with no m=video anywhere) — the §9.12.1 broadcast INVITE form.
func isAudioOnlyOffer(sdp string) bool {
	hasAudio, hasVideo := false, false
	for _, line := range strings.Split(sdp, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "m=video "):
			hasVideo = true
		case strings.HasPrefix(line, "m=audio "):
			hasAudio = true
		}
	}
	return hasAudio && !hasVideo
}

// randUint32 draws a random u32 from crypto/rand (session RTP
// identities must not be predictable across restarts).
func randUint32() uint32 {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return uint32(time.Now().UnixNano())
	}
	return binary.BigEndian.Uint32(b[:])
}

// handleBroadcastAck processes the device's 语音广播应答 MESSAGE
// (A.2.6.11): informational for the armed session (the delivery hinges
// on the INVITE that follows), logged for observability.
func (s *Server) handleBroadcastAck(payload manscdp.BroadcastResponse, fromUser string) {
	result := payload.Result
	slog.Info("gb28181: broadcast acknowledgement", "device", payload.DeviceID, "result", result, "from", fromUser)
}
