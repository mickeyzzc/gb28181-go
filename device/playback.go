// implements playback/download streaming of recorded
// segments for GB/T 28181 INVITE sessions (s=Playback / s=Download).

package device

import (
	"context"
	"log/slog"
	"math"
	"net"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

// PlaybackIndex extends RecordingIndex with the storage root needed to
// open segment files for playback. The concrete recording index satisfies
// it; RecordInfo-only fakes do not, and playback INVITEs against them are
// rejected with 488.
type PlaybackIndex interface {
	RecordingIndex
	Root() string
}

// parseSDPTimeRange extracts the t= line from an SDP body as a unix
// millisecond range. A missing or malformed t= line, or "t=0 0", means
// "all" and returns (0, math.MaxInt64).
func parseSDPTimeRange(body string) (startMs, endMs int64) {
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "t=") {
			continue
		}
		fields := strings.Fields(strings.TrimPrefix(line, "t="))
		if len(fields) < 2 {
			return 0, math.MaxInt64
		}
		start, err1 := strconv.ParseInt(fields[0], 10, 64)
		end, err2 := strconv.ParseInt(fields[1], 10, 64)
		if err1 != nil || err2 != nil {
			return 0, math.MaxInt64
		}
		if start == 0 && end == 0 {
			return 0, math.MaxInt64
		}
		return start * 1000, end * 1000
	}
	return 0, math.MaxInt64
}

// runPlayback streams recorded segments to the platform for a
// Playback/Download session. Playback paces frames to real time using the
// recorded PTS offsets relative to the first sent frame; Download sends as
// fast as possible. ctlCh carries SIP INFO PlaybackControl commands
// (PAUSE/PLAY/seek/speed) from the server. It stops when mediaCtx is
// cancelled (BYE or a replacing INVITE) or when the requested range is
// exhausted (unless paused, in which case it holds for a seek or BYE).
func (s *Server) runPlayback(mediaCtx context.Context, mediaConn *net.UDPConn, mediaTCPConn *net.TCPConn, rtpDest *net.UDPAddr, ssrc uint32, segments []SegmentMeta, root string, startMs, endMs int64, sessionType string, ctlCh <-chan PlaybackControl) {
	pusher := NewRtpPusher(mediaConn, rtpDest)
	if mediaTCPConn != nil {
		pusher.SetTCPConn(mediaTCPConn)
	}

	// The index returns segments in append order; sort by start time so
	// playback follows the recording chronology.
	sort.Slice(segments, func(i, j int) bool { return segments[i].StartMS < segments[j].StartMS })

	idx, _ := s.recordingIndex.(PlaybackIndex)

	var baseWall time.Time
	var basePts int64 // unix ms of the first sent AU
	started := false
	paused := false
	speed := 1.0

	// applyControl applies one PlaybackControl command to the pacing
	// state. auAbs is the absolute time of the frame currently held. It
	// returns a non-nil control when the caller must seek (PLAY with
	// StartTime): the outer loop re-looks-up segments and restarts.
	applyControl := func(ctl PlaybackControl, auAbs int64) *PlaybackControl {
		switch ctl.Value {
		case "PAUSE":
			paused = true
		case "PLAY":
			if paused {
				// Re-anchor pacing so the held frame goes out now and the
				// next at now + nominal gap (no burst after resume).
				baseWall = time.Now()
				basePts = auAbs
			}
			paused = false
		default:
			// Unknown control value — ignore.
			return nil
		}
		if ctl.Speed != nil && *ctl.Speed > 0 && *ctl.Speed != speed {
			speed = *ctl.Speed
			// Re-anchor so a speed change doesn't burst: the held frame
			// goes out now and the next at now + nominal gap / speed.
			baseWall = time.Now()
			basePts = auAbs
		}
		if ctl.Value == "PLAY" && ctl.StartTime != nil {
			return &ctl
		}
		return nil
	}

	// waitWhilePaused blocks until a control arrives or the media context
	// is done. It returns a seek request (non-nil) or ok=false when done.
	waitWhilePaused := func(auAbs int64) (*PlaybackControl, bool) {
		for paused {
			select {
			case <-mediaCtx.Done():
				return nil, false
			case ctl := <-ctlCh:
				if seek := applyControl(ctl, auAbs); seek != nil {
					return seek, true
				}
			}
		}
		return nil, true
	}

	// waitToSend blocks until the frame at auAbs may be sent, processing
	// control commands meanwhile. It returns a seek request (non-nil) when
	// the caller must restart from a new position, or ok=false when the
	// media context is done.
	waitToSend := func(auAbs int64) (*PlaybackControl, bool) {
		for {
			if seek, ok := waitWhilePaused(auAbs); !ok {
				return nil, false
			} else if seek != nil {
				return seek, true
			}
			if sessionType != "Playback" {
				// Download: no pacing; yield so other goroutines aren't starved.
				runtime.Gosched()
				return nil, true
			}
			wait := baseWall.Add(time.Duration(float64(auAbs-basePts)/speed) * time.Millisecond)
			if d := time.Until(wait); d > 0 {
				timer := time.NewTimer(d)
				select {
				case <-mediaCtx.Done():
					timer.Stop()
					return nil, false
				case ctl := <-ctlCh:
					timer.Stop()
					if seek := applyControl(ctl, auAbs); seek != nil {
						return seek, true
					}
					// State changed (paused/speed); re-evaluate.
				case <-timer.C:
					return nil, true
				}
			} else {
				return nil, true
			}
		}
	}

	// runPass streams the current segment range, returning a seek request
	// or nil when the range is exhausted or the media context is done.
	runPass := func() *PlaybackControl {
		for _, seg := range segments {
			reader, err := OpenSegment(filepath.Join(root, seg.File))
			if err != nil {
				slog.Warn("gb28181: playback open segment failed", "file", seg.File, "error", err)
				continue
			}
			for {
				nalus, pts, key, err := reader.Next()
				if err != nil {
					break // segment exhausted
				}
				auAbs := seg.StartMS + pts.Milliseconds()
				if auAbs < startMs {
					continue // skip to the requested start
				}
				if auAbs > endMs {
					// Past the requested end. If paused, hold the session
					// open for a seek or BYE; otherwise complete.
					if !paused {
						return nil
					}
					if seek, ok := waitWhilePaused(auAbs); !ok {
						return nil
					} else if seek != nil {
						return seek
					}
					continue
				}
				if seek, ok := waitToSend(auAbs); !ok {
					return nil
				} else if seek != nil {
					return seek
				}
				if !started {
					if !key {
						// The decoder needs an IDR first — fast-forward to the
						// next keyframe within the requested range.
						for {
							nalus, pts, key, err = reader.Next()
							if err != nil {
								break
							}
							auAbs = seg.StartMS + pts.Milliseconds()
							if auAbs > endMs {
								return nil
							}
							if key {
								break
							}
						}
					}
					if err != nil {
						break
					}
					baseWall = time.Now()
					basePts = auAbs
					started = true
				}
				auTime := time.UnixMilli(auAbs)
				psData := MuxH264ToPS(nalus, key, auTime, auTime)
				if err := pusher.SendFrame(psData, key, auTime, ssrc); err != nil {
					slog.Warn("gb28181: playback send failed", "error", err)
					return nil
				}
			}
		}
		// All segments exhausted. If paused, hold the session open for a
		// seek or BYE.
		if seek, ok := waitWhilePaused(0); !ok {
			return nil
		} else if seek != nil {
			return seek
		}
		return nil
	}

	for {
		seek := runPass()
		if seek == nil {
			break
		}
		// PLAY with StartTime: restart from the new position. Re-lookup
		// segments covering the new range and re-enter the loop; the
		// !started fast-forward rule applies to the new position.
		if seek.StartTime != nil {
			startMs = *seek.StartTime
		}
		if seek.EndTime != nil {
			endMs = *seek.EndTime
		}
		if idx == nil {
			slog.Warn("gb28181: playback seek without index", "start", startMs)
			break
		}
		segments = idx.Lookup(startMs, endMs)
		if len(segments) == 0 {
			slog.Warn("gb28181: playback seek found no covering recordings", "start", startMs, "end", endMs)
			break
		}
		sort.Slice(segments, func(i, j int) bool { return segments[i].StartMS < segments[j].StartMS })
		started = false
		paused = false
		slog.Info("gb28181: playback seek", "start", startMs, "end", endMs, "segments", len(segments))
	}
	slog.Info("gb28181: playback stream complete", "session", sessionType, "segments", len(segments))
}
