package device

// Unit tests for the §9.2 talkback offer classifier — table-driven mirrors
// of gb28181-rs parse_invite's media-kind logic (see talkback_test.go for
// the wire-level suite).

import "testing"

func TestParseTalkbackOffer(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantNil bool
		wantOK  bool
		wantTCP bool
		codec   AudioCodec
		ssrc    uint32
	}{
		{
			name:   "audio-only PCMA",
			body:   "v=0\r\no=- 0 0 IN IP4 10.0.0.1\r\ns=Play\r\nc=IN IP4 10.0.0.1\r\nt=0 0\r\nm=audio 30000 RTP/AVP 8\r\na=sendonly\r\ny=777\r\n",
			wantOK: true,
			codec:  AudioPCMA,
			ssrc:   777,
		},
		{
			name:   "audio-only PCMU",
			body:   "v=0\r\nm=audio 30000 RTP/AVP 0\r\ny=888\r\n",
			wantOK: true,
			codec:  AudioPCMU,
			ssrc:   888,
		},
		{
			name:   "payload-type order wins (8 before 0)",
			body:   "v=0\r\nm=audio 30000 RTP/AVP 8 0\r\ny=1\r\n",
			wantOK: true,
			codec:  AudioPCMA,
			ssrc:   1,
		},
		{
			name:   "payload-type order wins (0 before 8)",
			body:   "v=0\r\nm=audio 30000 RTP/AVP 0 8\r\ny=1\r\n",
			wantOK: true,
			codec:  AudioPCMU,
			ssrc:   1,
		},
		{
			name:   "leading-zero decimal y= SSRC",
			body:   "v=0\r\nm=audio 30000 RTP/AVP 8\r\ny=0200006001",
			wantOK: true,
			codec:  AudioPCMA,
			ssrc:   200006001,
		},
		{
			name:   "missing y= falls back to ssrc 0",
			body:   "v=0\r\nm=audio 30000 RTP/AVP 8\r\n",
			wantOK: true,
			codec:  AudioPCMA,
			ssrc:   0,
		},
		{
			name:   "non-G.711 payload type without canonical rtpmap",
			body:   "v=0\r\nm=audio 30000 RTP/AVP 96\r\na=rtpmap:96 L16/8000\r\ny=1\r\n",
			wantOK: false,
			ssrc:   1,
		},
		{
			// Parity quirk with the Rust twin: the rtpmap fallback only
			// recognizes values naming the canonical payload types
			// ("8 PCMA…" / "0 PCMU…").
			name:   "rtpmap fallback with canonical value",
			body:   "v=0\r\nm=audio 30000 RTP/AVP 96\r\na=rtpmap:8 PCMA/8000\r\ny=1\r\n",
			wantOK: true,
			codec:  AudioPCMA,
			ssrc:   1,
		},
		{
			name:   "rtpmap fallback non-canonical payload type is not G.711",
			body:   "v=0\r\nm=audio 30000 RTP/AVP 96\r\na=rtpmap:96 PCMA/8000\r\ny=1\r\n",
			wantOK: false,
			ssrc:   1,
		},
		{
			name:    "TCP media flagged",
			body:    "v=0\r\nm=audio 30000 TCP/RTP/AVP 8\r\ny=1\r\n",
			wantOK:  true,
			wantTCP: true,
			codec:   AudioPCMA,
			ssrc:    1,
		},
		{
			name:    "mixed audio+video offer stays a video session",
			body:    "v=0\r\nm=audio 30000 RTP/AVP 8\r\nm=video 30002 RTP/AVP 96\r\ny=1\r\n",
			wantNil: true,
		},
		{
			name:    "video-only offer is not talkback",
			body:    "v=0\r\nm=video 30002 RTP/AVP 96\r\ny=1\r\n",
			wantNil: true,
		},
		{
			name:    "no media lines is not talkback",
			body:    "v=0\r\ns=Play\r\n",
			wantNil: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseTalkbackOffer(tt.body)
			if tt.wantNil {
				if got != nil {
					t.Fatalf("parseTalkbackOffer = %+v, want nil", got)
				}
				return
			}
			if got == nil {
				t.Fatal("parseTalkbackOffer = nil, want an offer")
			}
			if got.codecOK != tt.wantOK {
				t.Fatalf("codecOK = %v, want %v", got.codecOK, tt.wantOK)
			}
			if tt.wantOK && got.codec != tt.codec {
				t.Fatalf("codec = %v, want %v", got.codec, tt.codec)
			}
			if got.tcp != tt.wantTCP {
				t.Fatalf("tcp = %v, want %v", got.tcp, tt.wantTCP)
			}
			if got.ssrc != tt.ssrc {
				t.Fatalf("ssrc = %d, want %d", got.ssrc, tt.ssrc)
			}
		})
	}
}

func TestAudioCodecNames(t *testing.T) {
	if AudioPCMA.PayloadType() != 8 || AudioPCMA.Name() != "PCMA" {
		t.Fatalf("PCMA = %d/%q", AudioPCMA.PayloadType(), AudioPCMA.Name())
	}
	if AudioPCMU.PayloadType() != 0 || AudioPCMU.Name() != "PCMU" {
		t.Fatalf("PCMU = %d/%q", AudioPCMU.PayloadType(), AudioPCMU.Name())
	}
}
