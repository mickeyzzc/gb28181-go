//go:build gb35114

// The device-side GB35114 A-level REGISTER state machine, wired into the
// device.Server through the RegisterAuthenticator seam (see
// device/authenticator.go):
//
//	REGISTER  (Authorization: Capability …)          device → platform
//	401       (WWW-Authenticate: … random1="…")      platform → device
//	REGISTER  (Authorization: Unidirection/Bidirection … sign1="…")
//	200 OK    (SecurityInfo: cryptkey [, sign2])     platform → device
//
// After the handshake the negotiated VKEK keys the Note-header integrity
// of every subsequent SIP request (OutgoingSigner).

package security35114

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/emmansun/gmsm/smx509"
)

// Options configures an Authenticator. Device, DeviceID and ServerID are
// required; the rest carry safe defaults.
type Options struct {
	// Device is the FDWSF SM2 signing identity (certificate + key).
	Device *Identity
	// PlatformCert is the SIP server's signing certificate, required to
	// verify sign2 of Bidirection handshakes.
	PlatformCert *smx509.Certificate
	// DeviceID is the 20-digit GB28181 device ID.
	DeviceID string
	// ServerID is the 20-digit SIP server (platform) ID.
	ServerID string
	// KeyVersion labels the device key in the Capability announcement;
	// defaults to the current UTC time in Date-header format.
	KeyVersion string
	// IncludeDeviceCert attaches the certificate PEM as cnonce in the
	// Capability announcement for platforms that do not pre-provision it.
	IncludeDeviceCert bool
	// RandomEncoding selects the signed-payload representation (default
	// ConcatWireStrings, matching observed captures).
	RandomEncoding RandomEncoding
	// Sign2Order selects the platform sign2 operand order (default
	// Sign2R1R2, the standard text order).
	Sign2Order Sign2Order
	// Rand supplies randomness for random2 (default crypto/rand).
	Rand io.Reader
}

// Authenticator implements the device.RegisterAuthenticator and
// device.OutgoingSigner seams for GB35114 A-level.
type Authenticator struct {
	opts Options

	mu      sync.Mutex
	mode    Mode
	random1 string
	random2 string
	vkek    []byte
}

// New validates the options and returns a ready Authenticator.
func New(opts Options) (*Authenticator, error) {
	if opts.Device == nil {
		return nil, errors.New("security35114: Options.Device is required")
	}
	if opts.DeviceID == "" || opts.ServerID == "" {
		return nil, errors.New("security35114: Options.DeviceID and Options.ServerID are required")
	}
	if opts.KeyVersion == "" {
		opts.KeyVersion = FormatDate(time.Now())
	}
	if opts.Rand == nil {
		opts.Rand = rand.Reader
	}
	return &Authenticator{opts: opts}, nil
}

// Mode reports the scheme negotiated by the last challenge ("" before one
// arrives).
func (a *Authenticator) Mode() Mode {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.mode
}

// VKEK returns the negotiated video-key-encryption-key, or nil before the
// handshake completes.
func (a *Authenticator) VKEK() []byte {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.vkek == nil {
		return nil
	}
	out := make([]byte, len(a.vkek))
	copy(out, a.vkek)
	return out
}

// InitialAuthorization implements device.RegisterAuthenticator: the
// Capability announcement of the first REGISTER.
func (a *Authenticator) InitialAuthorization() string {
	if !a.opts.IncludeDeviceCert {
		return BuildCapabilityAuthorization(a.opts.KeyVersion, "")
	}
	return BuildCapabilityAuthorization(a.opts.KeyVersion, a.opts.Device.CertPEM)
}

// AuthorizeWithChallenge implements device.RegisterAuthenticator: it
// consumes the 401 challenge, draws random2, signs and returns the
// Authorization header for the retried REGISTER.
func (a *Authenticator) AuthorizeWithChallenge(wwwAuthenticate string) (string, error) {
	ch, err := ParseChallenge(wwwAuthenticate)
	if err != nil {
		return "", err
	}
	random2, err := a.newRandom()
	if err != nil {
		return "", err
	}
	payload := SignAuthPayload(ch.Random1, random2, a.opts.ServerID, a.opts.RandomEncoding)
	sign1, err := SignMessage(a.opts.Device.PrivateKey, payload)
	if err != nil {
		return "", fmt.Errorf("security35114: signing challenge: %w", err)
	}

	a.mu.Lock()
	a.mode, a.random1, a.random2 = ch.Mode, ch.Random1, random2
	a.mu.Unlock()

	return BuildAuthAuthorization(ch, random2, a.opts.ServerID, a.opts.DeviceID, sign1), nil
}

// VerifyOK implements device.RegisterAuthenticator: it opens the cryptkey
// envelope of the 200 OK, and for Bidirection additionally verifies the
// platform's sign2 against the pre-provisioned platform certificate and
// the handshake state.
func (a *Authenticator) VerifyOK(securityInfo string) error {
	a.mu.Lock()
	mode, random1, random2 := a.mode, a.random1, a.random2
	a.mu.Unlock()
	if mode == "" {
		return errors.New("security35114: SecurityInfo received before a challenge was answered")
	}

	si, err := ParseSecurityInfo(securityInfo)
	if err != nil {
		return err
	}
	vkek, err := DecryptVKEK(a.opts.Device.PrivateKey, si.CryptKey)
	if err != nil {
		return fmt.Errorf("security35114: opening cryptkey: %w", err)
	}
	if si.Mode == ModeBidirection {
		if a.opts.PlatformCert == nil {
			return errors.New("security35114: Bidirection requires Options.PlatformCert to verify sign2")
		}
		// The echoed handshake fields must match this handshake exactly —
		// anything else is a replay or a foreign session.
		if si.Random1 != random1 || si.Random2 != random2 ||
			si.DeviceID != a.opts.DeviceID || si.ServerID != a.opts.ServerID {
			return errors.New("security35114: SecurityInfo handshake fields do not match this handshake")
		}
		payload := Sign2Payload(si.Random1, si.Random2, si.DeviceID, si.CryptKey, a.opts.Sign2Order, a.opts.RandomEncoding)
		if err := VerifyMessage(a.opts.PlatformCert, payload, si.Sign2); err != nil {
			return fmt.Errorf("security35114: verifying sign2: %w", err)
		}
	}

	a.mu.Lock()
	a.vkek = vkek
	a.mu.Unlock()
	return nil
}

// DecorateOutgoing implements device.OutgoingSigner: it stamps the Date
// and Note headers carrying the keyed-SM3 digest onto every non-REGISTER
// request. Messages before the handshake completes, or REGISTER itself,
// pass through untouched.
func (a *Authenticator) DecorateOutgoing(method, from, to, callID, body string) (date string, note string) {
	a.mu.Lock()
	vkek := a.vkek
	a.mu.Unlock()
	if vkek == nil || method == "REGISTER" {
		return "", ""
	}
	date = FormatDate(time.Now())
	note = BuildNoteHeader(method, from, to, callID, date, vkek, body, VKEKRaw)
	return date, note
}

// newRandom draws the 128-bit random2.
func (a *Authenticator) newRandom() (string, error) {
	buf := make([]byte, 16)
	if _, err := io.ReadFull(a.opts.Rand, buf); err != nil {
		return "", fmt.Errorf("security35114: drawing random2: %w", err)
	}
	return base64.StdEncoding.EncodeToString(buf), nil
}
