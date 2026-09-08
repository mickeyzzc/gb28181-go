//go:build gb35114

// The platform-side (UAS) GB35114 A-level REGISTER state machine — the
// counterpart of the device-side Authenticator:
//
//	REGISTER  (Authorization: Capability …)          device → platform
//	401       (WWW-Authenticate: … random1="…")      platform → device   Challenge
//	REGISTER  (Authorization: Unidirection/Bidirection … sign1="…")
//	200 OK    (SecurityInfo: cryptkey [, sign2])     platform → device   VerifyRegister
//
// After the handshake the negotiated VKEK keys the Note-header integrity
// of every subsequent device request (VerifyNote). The type is safe for
// concurrent use; sessions are keyed by device ID.

package security35114

import (
	"crypto/ecdsa"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/emmansun/gmsm/smx509"
)

// Platform-side failure modes, mapped by SIP servers onto 4xx responses.
var (
	// ErrNoChallenge: VerifyRegister arrived with no pending challenge for
	// the device (no REGISTER preceded it, or the state was reset).
	ErrNoChallenge = errors.New("security35114: no pending challenge for device")
	// ErrChallengeMismatch: the Authorization echoes a random1 that is not
	// the pending one — a replayed or forged handshake.
	ErrChallengeMismatch = errors.New("security35114: Authorization random1 does not match the pending challenge")
	// ErrDeviceCert: no trusted certificate for the device — neither
	// pre-provisioned nor announced via the Capability cnonce.
	ErrDeviceCert = errors.New("security35114: no trusted certificate for device")
	// ErrServerIDMismatch: the device signed for a different SIP server ID.
	ErrServerIDMismatch = errors.New("security35114: Authorization signed for a different server ID")
	// ErrNoSession: no completed handshake under this device ID.
	ErrNoSession = errors.New("security35114: no negotiated VKEK for device")
)

// PlatformConfig configures a Platform. ServerID and at least one device
// certificate source (DeviceCerts or the Capability cnonce) are required.
type PlatformConfig struct {
	// ServerID is the platform's 20-digit SIP server ID, echoed in
	// challenges and SecurityInfo.
	ServerID string
	// Identity is the platform SM2 signing identity, required for
	// Bidirection challenges (it produces sign2).
	Identity *Identity
	// DeviceCerts pre-provisions FDWSF signing certificates by device ID.
	// A certificate announced via the Capability cnonce is used when no
	// pre-provisioned entry exists.
	DeviceCerts map[string]*smx509.Certificate
	// Mode selects the challenge scheme; default is Bidirection when
	// Identity is set and Unidirection otherwise.
	Mode Mode
	// RandomEncoding selects the signed-payload representation (default
	// ConcatWireStrings, matching observed captures).
	RandomEncoding RandomEncoding
	// Sign2Order selects the sign2 operand order (default Sign2R1R2, the
	// standard text order).
	Sign2Order Sign2Order
	// VKEKEncoding selects how the VKEK enters the Note digest (default
	// VKEKRaw).
	VKEKEncoding VKEKEncoding
	// Rand supplies randomness for random1, the VKEK, and the SM2
	// encryption ephemeral (default crypto/rand).
	Rand io.Reader
	// VKEKLen is the negotiated key length in bytes (default 16).
	VKEKLen int
}

// platformSession is the per-device handshake state.
type platformSession struct {
	mode             Mode
	random1          string
	announcedCert    *smx509.Certificate // from Capability cnonce, if any
	vkek             []byte              // active once a handshake completed
	lastAuth         string              // completed Authorization (retransmission cache)
	lastSecurityInfo string
}

// Platform implements the platform (UAS) side of GB35114 A-level. Its
// method set matches platform/sip's RegisterAuthenticator seam, so it can
// be wired directly into that server.
type Platform struct {
	cfg PlatformConfig

	mu       sync.Mutex
	sessions map[string]*platformSession
}

// NewPlatform validates the configuration and returns a ready Platform.
func NewPlatform(cfg PlatformConfig) (*Platform, error) {
	if cfg.ServerID == "" {
		return nil, errors.New("security35114: PlatformConfig.ServerID is required")
	}
	mode := cfg.Mode
	switch mode {
	case "":
		if cfg.Identity != nil {
			mode = ModeBidirection
		} else {
			mode = ModeUnidirection
		}
	case ModeBidirection:
		if cfg.Identity == nil {
			return nil, errors.New("security35114: Bidirection requires PlatformConfig.Identity")
		}
	case ModeUnidirection:
	default:
		return nil, fmt.Errorf("security35114: unknown mode %q", mode)
	}
	cfg.Mode = mode
	if cfg.VKEKLen <= 0 {
		cfg.VKEKLen = 16
	}
	if cfg.Rand == nil {
		cfg.Rand = rand.Reader
	}
	return &Platform{cfg: cfg, sessions: map[string]*platformSession{}}, nil
}

// Challenge records the Capability announcement (parsing an optional cnonce
// device certificate) and issues a fresh random1 challenge for the device.
// It returns the complete WWW-Authenticate header value for the 401. An
// already-negotiated VKEK stays active while the device re-registers, so
// keepalives between the challenge and the completing REGISTER still verify.
func (p *Platform) Challenge(deviceID, capabilityAuthorization string) (string, error) {
	random1, err := p.newRandom()
	if err != nil {
		return "", err
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	s := p.sessions[deviceID]
	if s == nil {
		s = &platformSession{}
		p.sessions[deviceID] = s
	}
	s.mode = p.cfg.Mode
	s.random1 = random1
	s.lastAuth, s.lastSecurityInfo = "", ""
	if capabilityAuthorization != "" {
		if ann, err := ParseCapabilityAuthorization(capabilityAuthorization); err == nil && ann.DeviceCertPEM != "" {
			if cert, err := ParseCertificatePEM([]byte(ann.DeviceCertPEM)); err == nil {
				s.announcedCert = cert
			}
		}
	}
	return BuildChallenge(p.cfg.Mode, random1), nil
}

// VerifyRegister validates the Authorization header of the retried REGISTER
// against the pending challenge and the device certificate, negotiates the
// VKEK, seals it to the device public key (cryptkey), and — for
// Bidirection — signs the response (sign2). It returns the complete
// SecurityInfo header value for the 200 OK. A byte-identical retransmission
// of the completed REGISTER returns the same SecurityInfo (SIP-over-UDP
// retransmission), while any other Authorization with a stale random1 is
// rejected as a replay.
func (p *Platform) VerifyRegister(deviceID, authorization string) (string, error) {
	aa, err := ParseAuthAuthorization(authorization)
	if err != nil {
		return "", err
	}

	p.mu.Lock()
	s := p.sessions[deviceID]
	p.mu.Unlock()
	if s == nil {
		return "", fmt.Errorf("%w: %s", ErrNoChallenge, deviceID)
	}

	p.mu.Lock()
	lastAuth, lastSecurityInfo, random1, mode := s.lastAuth, s.lastSecurityInfo, s.random1, s.mode
	p.mu.Unlock()
	if authorization == lastAuth {
		return lastSecurityInfo, nil // idempotent retransmission
	}
	if aa.Random1 != random1 {
		return "", fmt.Errorf("%w: %s", ErrChallengeMismatch, deviceID)
	}
	if aa.Mode != mode {
		return "", fmt.Errorf("security35114: Authorization scheme %q does not answer the %q challenge", aa.Mode, mode)
	}
	if aa.ServerID != p.cfg.ServerID {
		return "", fmt.Errorf("%w: %q", ErrServerIDMismatch, aa.ServerID)
	}
	if aa.DeviceID != "" && aa.DeviceID != deviceID {
		return "", fmt.Errorf("security35114: Authorization deviceid %q does not match the registering device %q", aa.DeviceID, deviceID)
	}

	cert := p.resolveCert(deviceID, s)
	if cert == nil {
		return "", fmt.Errorf("%w: %s", ErrDeviceCert, deviceID)
	}
	pub, ok := cert.PublicKey.(*ecdsa.PublicKey)
	if !ok {
		return "", fmt.Errorf("%w: %s: public key is %T", ErrDeviceCert, deviceID, cert.PublicKey)
	}

	payload := SignAuthPayload(aa.Random1, aa.Random2, aa.ServerID, p.cfg.RandomEncoding)
	if err := VerifyMessage(cert, payload, aa.Sign1); err != nil {
		return "", fmt.Errorf("security35114: verifying sign1 for %s: %w", deviceID, err)
	}

	vkek := make([]byte, p.cfg.VKEKLen)
	if _, err := io.ReadFull(p.cfg.Rand, vkek); err != nil {
		return "", fmt.Errorf("security35114: drawing VKEK: %w", err)
	}
	cryptKey, err := EncryptVKEK(p.cfg.Rand, pub, vkek)
	if err != nil {
		return "", fmt.Errorf("security35114: sealing VKEK: %w", err)
	}
	si := SecurityInfo{Mode: aa.Mode, Algorithm: CapabilityAlgorithm, CryptKey: cryptKey}
	if aa.Mode == ModeBidirection {
		si.Random1, si.Random2 = aa.Random1, aa.Random2
		si.DeviceID, si.ServerID = deviceID, p.cfg.ServerID
		sign2, err := SignMessage(p.cfg.Identity.PrivateKey, Sign2Payload(aa.Random1, aa.Random2, deviceID, cryptKey, p.cfg.Sign2Order, p.cfg.RandomEncoding))
		if err != nil {
			return "", fmt.Errorf("security35114: signing sign2: %w", err)
		}
		si.Sign2 = sign2
	}
	securityInfo := BuildSecurityInfo(si)

	p.mu.Lock()
	s.vkek = vkek
	s.lastAuth, s.lastSecurityInfo = authorization, securityInfo
	p.mu.Unlock()
	return securityInfo, nil
}

// VerifyNote validates the Note header of a subsequent request from the
// device. An empty note passes (the request carries no integrity header —
// e.g. a device that has not completed the handshake); a present but
// invalid one fails.
func (p *Platform) VerifyNote(deviceID, note, method, from, to, callID, date, body string) error {
	if note == "" {
		return nil
	}
	p.mu.Lock()
	s := p.sessions[deviceID]
	p.mu.Unlock()
	if s == nil || s.vkek == nil {
		return fmt.Errorf("%w: %s", ErrNoSession, deviceID)
	}
	return VerifyNoteHeader(note, method, from, to, callID, date, s.vkek, body, p.cfg.VKEKEncoding)
}

// VKEK returns a copy of the device's negotiated VKEK, or nil before the
// handshake completed.
func (p *Platform) VKEK(deviceID string) []byte {
	p.mu.Lock()
	defer p.mu.Unlock()
	s := p.sessions[deviceID]
	if s == nil || s.vkek == nil {
		return nil
	}
	out := make([]byte, len(s.vkek))
	copy(out, s.vkek)
	return out
}

// resolveCert picks the device signing certificate: a pre-provisioned
// entry wins, the Capability cnonce announcement is the fallback.
func (p *Platform) resolveCert(deviceID string, s *platformSession) *smx509.Certificate {
	if cert, ok := p.cfg.DeviceCerts[deviceID]; ok {
		return cert
	}
	return s.announcedCert
}

// newRandom draws the 128-bit random1.
func (p *Platform) newRandom() (string, error) {
	buf := make([]byte, 16)
	if _, err := io.ReadFull(p.cfg.Rand, buf); err != nil {
		return "", fmt.Errorf("security35114: drawing random1: %w", err)
	}
	return base64.StdEncoding.EncodeToString(buf), nil
}
