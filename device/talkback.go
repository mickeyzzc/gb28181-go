package device

// GB/T 28181-2022 §9.2 voice talkback, receive half: the platform sends
// G.711 audio to the device. An audio-only INVITE offer (m=audio, no
// m=video anywhere) opens a talkback session; the device answers with an
// ephemeral UDP receive port and forwards RTP audio payload to the host
// sink. Ported from the gb28181-rs reference implementation, which is
// production-verified against a real NVR's talkback offer (payload-type-
// only PCMA without a=rtpmap, leading-zero decimal y= SSRC).

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"time"
)

// AudioCodec is the G.711 variant negotiated for talkback: the RTP
// payload type doubles as the codec identity (8 = A-law, 0 = μ-law).
type AudioCodec int

const (
	// AudioPCMU is G.711 μ-law (RTP payload type 0).
	AudioPCMU AudioCodec = 0
	// AudioPCMA is G.711 A-law (RTP payload type 8).
	AudioPCMA AudioCodec = 8
)

// PayloadType returns the RTP payload type used on the wire.
func (c AudioCodec) PayloadType() int { return int(c) }

// Name returns the SDP rtpmap encoding name ("PCMA" / "PCMU").
func (c AudioCodec) Name() string {
	if c == AudioPCMA {
		return "PCMA"
	}
	return "PCMU"
}

// TalkbackSink consumes G.711 audio received on a talkback session
// (issue #80). OnAudio fires on the session's receive goroutine with the
// RTP payload (headers stripped), the packet SSRC (falling back to the
// session SSRC negotiated in the offer), and the negotiated codec;
// implementations must be safe for concurrent use.
type TalkbackSink interface {
	OnAudio(payload []byte, ssrc uint32, codec AudioCodec)
}

// SetTalkbackSink installs the audio talkback consumer (receive half of
// §9.2 voice talkback). Without a sink, audio-only INVITEs are refused
// with 488 — receiving audio nobody consumes would be a silent black
// hole. Call before Start.
func (s *Server) SetTalkbackSink(sink TalkbackSink) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.audioSink = sink
}

// SetTalkbackSource installs the upstream half of §9.2 voice talkback
// (issue #83): a channel of pre-framed G.711 bytes the device sends as
// RTP toward the platform's media address on the talk session. Frames
// are drained at one per 20 ms tick — push ~160-byte frames (20 ms of
// 8 kHz G.711) at a real-time cadence; the channel buffers bursts. An
// offer that requires upstream audio (a=recvonly) without a source is
// refused with 488, mirroring the no-sink refusal.
func (s *Server) SetTalkbackSource(frames <-chan []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.audioSource = frames
}

// talkbackOffer is the parsed form of an audio-only INVITE SDP offer.
type talkbackOffer struct {
	codec     AudioCodec // valid iff codecOK
	codecOK   bool
	ssrc      uint32
	tcp       bool
	connIP    string // c= address — the upstream RTP target
	mediaPort int    // m=audio port — the upstream RTP target
	recvOnly  bool   // a=recvonly: platform receives only — device must send
	sendOnly  bool   // a=sendonly: platform sends only — receive-only session
}

// parseTalkbackOffer classifies an INVITE SDP body: m=audio with no m=
// video anywhere is a talkback offer (GB/T 28181-2022 §9.2) and the
// device receives audio. Returns nil for video or mixed offers (those
// keep the video-push behavior). Mirrors gb28181-rs parse_invite's
// media-kind logic.
func parseTalkbackOffer(body string) *talkbackOffer {
	var hasVideo, hasAudio bool
	var payloadTypes []int
	var mLineProto string
	var rtpmaps []string
	offer := &talkbackOffer{}
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "m=video "):
			hasVideo = true
		case strings.HasPrefix(line, "m=audio "):
			hasAudio = true
			parts := strings.Fields(line)
			if len(parts) >= 3 {
				mLineProto = parts[2]
				if port, err := strconv.Atoi(parts[1]); err == nil {
					offer.mediaPort = port
				}
				for _, p := range parts[3:] {
					if pt, err := strconv.Atoi(p); err == nil {
						payloadTypes = append(payloadTypes, pt)
					}
				}
			}
		case strings.HasPrefix(line, "c=IN IP4 "):
			if offer.connIP == "" {
				offer.connIP = strings.TrimSpace(strings.TrimPrefix(line, "c=IN IP4 "))
			}
		case line == "a=recvonly":
			offer.recvOnly = true
		case line == "a=sendonly":
			offer.sendOnly = true
		case strings.HasPrefix(line, "a=rtpmap:"):
			rtpmaps = append(rtpmaps, strings.TrimPrefix(line, "a=rtpmap:"))
		case strings.HasPrefix(line, "y="):
			// Leading-zero decimal SSRCs parse as their numeric value.
			if v, err := strconv.ParseUint(strings.TrimPrefix(line, "y="), 10, 32); err == nil {
				offer.ssrc = uint32(v)
			}
		}
	}
	if hasVideo || !hasAudio {
		return nil
	}
	offer.tcp = strings.Contains(mLineProto, "TCP")
	// Codec by payload type first (8 = PCMA, 0 = PCMU), in offer order.
	for _, pt := range payloadTypes {
		switch pt {
		case int(AudioPCMA):
			offer.codec, offer.codecOK = AudioPCMA, true
		case int(AudioPCMU):
			offer.codec, offer.codecOK = AudioPCMU, true
		}
		if offer.codecOK {
			break
		}
	}
	if !offer.codecOK {
		// Payload types outside {0,8}: fall back to an rtpmap naming the
		// canonical type (parity with the Rust twin — only "8 PCMA…" /
		// "0 PCMU…" values count).
		for _, rm := range rtpmaps {
			if strings.HasPrefix(rm, "8 PCMA") {
				offer.codec, offer.codecOK = AudioPCMA, true
				break
			}
			if strings.HasPrefix(rm, "0 PCMU") {
				offer.codec, offer.codecOK = AudioPCMU, true
				break
			}
		}
	}
	return offer
}

// buildTalkbackSDP builds the SDP answer for an audio-only talkback
// INVITE (§9.2): the device advertises the UDP port its RTP receive loop
// is bound to and mirrors the offered G.711 codec. Byte-for-byte twin of
// gb28181-rs build_audio_sdp_answer. direction is an optional direction
// attribute line (with CRLF) inserted before y= — only the upstream form
// sets it (offer a=recvonly → answer a=sendonly); every other answer
// stays directionless, byte-identical to the pre-upstream wire form.
func buildTalkbackSDP(deviceIP string, mediaPort int, ssrc uint32, codec AudioCodec, direction string) string {
	pt := codec.PayloadType()
	return fmt.Sprintf("v=0\r\no=- 0 0 IN IP4 %s\r\ns=Play\r\nc=IN IP4 %s\r\nt=0 0\r\n"+
		"m=audio %d RTP/AVP %d\r\na=rtpmap:%d %s/8000\r\n%sy=%d\r\n",
		deviceIP, deviceIP, mediaPort, pt, pt, codec.Name(), direction, ssrc)
}

// handleTalkbackInvite serves an audio-only INVITE (talkback receive,
// §9.2): answer 200 OK with an ephemeral UDP port and forward G.711 RTP
// payload to the configured TalkbackSink. Refused with 488 when no sink
// is registered, the codec is not G.711 A/μ-law, or the offer asks for
// TCP media (UDP only in this revision). The talkback dialog occupies
// the same single-dialog slot as video sessions — an audio INVITE
// recycles any active video session and vice versa, and BYE tears the
// receiver down through the shared media cleanup.
func (s *Server) handleTalkbackInvite(ctx context.Context, msg SipMessage, offer *talkbackOffer, fromAddr net.Addr) {
	s.mu.Lock()
	sink := s.audioSink
	source := s.audioSource
	s.mu.Unlock()

	if sink == nil {
		slog.Warn("gb28181: talkback INVITE but no audio sink configured — 488")
		s.rejectInvite488(msg, fromAddr)
		return
	}
	if !offer.codecOK {
		slog.Warn("gb28181: talkback INVITE with non-G.711 codec — 488")
		s.rejectInvite488(msg, fromAddr)
		return
	}
	if offer.tcp {
		slog.Warn("gb28181: talkback over TCP media unsupported — 488")
		s.rejectInvite488(msg, fromAddr)
		return
	}
	// Upstream-required offers (a=recvonly — the platform only listens)
	// must not be answered by a device that cannot send: mirror the
	// no-sink refusal (issue #83).
	if offer.recvOnly && source == nil {
		slog.Warn("gb28181: talkback INVITE requires upstream audio but no talkback source configured — 488")
		s.rejectInvite488(msg, fromAddr)
		return
	}

	slog.Info("gb28181: talkback INVITE", "from", fromAddr.String(), "codec", offer.codec.Name(), "ssrc", offer.ssrc)

	// Recycle any previous media session (shared single-dialog slot).
	s.mu.Lock()
	s.teardownMediaLocked()
	s.mu.Unlock()

	mediaConn, err := net.ListenUDP("udp", nil)
	if err != nil {
		slog.Warn("gb28181: failed to bind talkback media UDP", "error", err)
		return
	}
	mediaPort := mediaConn.LocalAddr().(*net.UDPAddr).Port

	// The answer's address is the real source IP toward the platform, not
	// an interface scan (wrong on multihomed hosts).
	localIPAddr, err := getLocalIP(ctx, fromAddr.String())
	if err != nil {
		slog.Warn("gb28181: failed to determine local IP, falling back to interface scan", "error", err)
		localIPAddr = localIP()
	}

	// Only the upstream-required form announces a direction — every other
	// answer stays byte-identical to the pre-upstream wire form.
	direction := ""
	if offer.recvOnly {
		direction = "a=sendonly\r\n"
	}
	sdp := buildTalkbackSDP(localIPAddr, int(mediaPort), offer.ssrc, offer.codec, direction)
	ok200 := Build200OK(msg, "application/sdp", sdp)
	if err := s.sendSIP(ok200.Serialize(), fromAddr); err != nil {
		slog.Warn("gb28181: failed to send talkback 200 OK", "error", err)
		_ = mediaConn.Close()
		return
	}

	// The receiver exits on socket close; the cancel links its lifetime to
	// the server context for the shared teardown contract.
	mediaCtx, mediaCancel := context.WithCancel(ctx)
	s.mu.Lock()
	s.mediaConn = mediaConn
	s.mediaCancel = mediaCancel
	s.mu.Unlock()

	// Upstream half (issue #83): with a source installed the device sends
	// G.711 RTP to the platform's media address, unless the offer is
	// explicitly receive-only-for-the-device (a=sendonly).
	if source != nil && !offer.sendOnly {
		dst := &net.UDPAddr{IP: net.ParseIP(offer.connIP), Port: offer.mediaPort}
		if dst.IP == nil {
			// No c= line — fall back to the SIP peer's address.
			if udpPeer, ok := fromAddr.(*net.UDPAddr); ok {
				dst.IP = udpPeer.IP
			}
		}
		if dst.IP != nil && dst.Port > 0 {
			go runTalkbackSender(mediaCtx, mediaConn, dst, offer.codec, source)
		} else {
			slog.Warn("gb28181: talkback offer lacks a usable c=/m= media address — upstream disabled")
		}
	}

	slog.Info("gb28181: talkback session receiving", "call_id", msg.CallID, "codec", offer.codec.Name(), "port", mediaPort)
	go func() {
		defer mediaCancel()
		runTalkbackReceiver(mediaConn, sink, offer.ssrc, offer.codec)
	}()
}

// runTalkbackSender paces the upstream half of a talkback session
// (issue #83): one source frame per 20 ms tick is packetized as RTP —
// payload type 8/0, 8 kHz clock (G.711: one byte per sample, so the
// timestamp advances by the payload length), random per-session
// SSRC/sequence/timestamp bases. Exits when the media context is
// cancelled (BYE / session recycle / server stop) or the socket breaks.
func runTalkbackSender(ctx context.Context, conn *net.UDPConn, dst *net.UDPAddr, codec AudioCodec, frames <-chan []byte) {
	pt := byte(codec.PayloadType())
	buf := make([]byte, 12+2048)
	ssrc := randUint32()
	seq := uint16(randUint32())
	ts := randUint32()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			select {
			case frame := <-frames:
				if len(frame) == 0 || len(frame) > 2048 {
					continue
				}
				buf[0] = 0x80 // V=2, no padding/extension/CSRC
				buf[1] = pt   // M=0 — continuous G.711 stream
				binary.BigEndian.PutUint16(buf[2:], seq)
				binary.BigEndian.PutUint32(buf[4:], ts)
				binary.BigEndian.PutUint32(buf[8:], ssrc)
				copy(buf[12:], frame)
				if _, err := conn.WriteToUDP(buf[:12+len(frame)], dst); err != nil {
					slog.Debug("gb28181: talkback send loop ended", "error", err)
					return
				}
				seq++
				ts += uint32(len(frame))
			default:
				// Nothing queued this ptime — nothing is sent.
			}
		}
	}
}

// randUint32 draws a random u32 from crypto/rand, falling back to a
// time-seeded value (randomTag's contract: session identities must not
// be predictable across restarts).
func randUint32() uint32 {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return uint32(time.Now().UnixNano())
	}
	return binary.BigEndian.Uint32(b[:])
}

// rejectInvite488 answers an INVITE with 488 Not Acceptable Here,
// echoing the request's dialog headers.
func (s *Server) rejectInvite488(msg SipMessage, fromAddr net.Addr) {
	reject := SipMessage{
		StatusCode: 488,
		Via:        msg.Via,
		From:       msg.From,
		To:         msg.To,
		CallID:     msg.CallID,
		CSeq:       msg.CSeq,
		Headers:    make(map[string]string),
	}
	if err := s.sendSIP(reject.Serialize(), fromAddr); err != nil {
		slog.Warn("gb28181: failed to send 488", "error", err)
	}
}

// runTalkbackReceiver pumps RTP audio off the session socket: strips the
// fixed 12-byte header plus any CSRC list and hands the payload to the
// sink. It exits when the socket closes (BYE teardown / session recycle
// / server stop) or on a hard receive error.
func runTalkbackReceiver(conn *net.UDPConn, sink TalkbackSink, sessionSSRC uint32, codec AudioCodec) {
	buf := make([]byte, 2048)
	for {
		n, _, err := conn.ReadFromUDP(buf)
		if err != nil {
			// Closed socket (BYE / recycle / stop) is the normal exit.
			slog.Debug("gb28181: talkback receive loop ended", "error", err)
			return
		}
		if n < 12 {
			continue
		}
		csrcCount := int(buf[0] & 0x0F)
		headerLen := 12 + csrcCount*4
		if n <= headerLen {
			continue
		}
		ssrc := binary.BigEndian.Uint32(buf[8:12])
		if ssrc == 0 {
			ssrc = sessionSSRC
		}
		payload := append([]byte(nil), buf[headerLen:n]...)
		sink.OnAudio(payload, ssrc, codec)
	}
}
