// Package hygiene_test holds source-scan regression guards that keep this
// library neutral and import-safe: the branding and wire-identity problems
// fixed in v0.3.0 must not silently reappear.
package hygiene_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mickeyzzc/gb28181-go/platform/cascade"
	"github.com/mickeyzzc/gb28181-go/platform/sip"
)

// libraryDirs are the importable packages of the module (examples/, tmp/,
// and conformance scratch harnesses are excluded).
var libraryDirs = []string{"device", "manscdp", "platform", "psmux", "nalutil"}

// bannedBranding lists product-identity string literals that must never be
// baked into library code — identity goes on the wire through configuration
// with neutral defaults.
var bannedBranding = []string{
	`"MiBee"`,          // old hardcoded catalog manufacturer
	`"MiBeeNvr"`,       // old hardcoded catalog model
	`MiBeeNvr-GB28181`, // old hardcoded SIP User-Agent (platform + cascade)
	`mibee-rec`,        // old hardcoded device User-Agent (Rust twin parity)
	`mibee%d`,          // old branded, time-based dialog To-tag
	`MiBee Camera`,     // old hardcoded catalog/device name
}

func productionGoFiles(t *testing.T) map[string]string {
	t.Helper()
	files := make(map[string]string)
	for _, dir := range libraryDirs {
		err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			files[path] = string(data)
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", dir, err)
		}
	}
	return files
}

func TestNoProductBrandingInLibraryCode(t *testing.T) {
	for path, src := range productionGoFiles(t) {
		for _, banned := range bannedBranding {
			if strings.Contains(src, banned) {
				t.Errorf("%s contains hardcoded branding %q — make it configurable with a neutral default", path, banned)
			}
		}
	}
}

func TestNoDirectStdoutPrintingInLibraryCode(t *testing.T) {
	// Library output goes through log/slog; fmt.Print* would leak into the
	// host's stdout uncontrolled.
	for path, src := range productionGoFiles(t) {
		for _, banned := range []string{"fmt.Println(", "fmt.Printf(", "log.Print("} {
			if strings.Contains(src, banned) {
				t.Errorf("%s uses %s — log through log/slog instead", path, banned)
			}
		}
	}
}

// TestUserAgentDefaultsAreNeutralAndConfigurable pins the wire identity
// contract: empty config → neutral default, never a product name.
func TestUserAgentDefaultsAreNeutralAndConfigurable(t *testing.T) {
	if got := (sip.Config{}).EffectiveUserAgent(); got != sip.DefaultUserAgent {
		t.Fatalf("platform default UA = %q, want %q", got, sip.DefaultUserAgent)
	}
	if strings.Contains(strings.ToLower(sip.DefaultUserAgent), "mibee") {
		t.Fatal("platform default UA must not contain product branding")
	}
	if got := (sip.Config{UserAgent: "host/1.0"}).EffectiveUserAgent(); got != "host/1.0" {
		t.Fatalf("platform override = %q", got)
	}

	if got := (cascade.Config{}).EffectiveUserAgent(); !strings.HasPrefix(got, "gb28181-go") {
		t.Fatalf("cascade default UA = %q, want neutral gb28181-go prefix", got)
	}
	if got := (cascade.Config{UserAgent: "host/2.0"}).EffectiveUserAgent(); got != "host/2.0" {
		t.Fatalf("cascade override = %q", got)
	}
}

// TestCatalogIdentityDefaultsAreNeutral pins the catalog/DeviceInfo fallback
// identity (was MiBee/MiBeeNvr).
func TestCatalogIdentityDefaultsAreNeutral(t *testing.T) {
	var c cascade.Config
	if got := c.CatalogManufacturer(); got != "Unknown" {
		t.Fatalf("catalog manufacturer default = %q, want Unknown", got)
	}
	if got := c.CatalogModel(); got != "Unknown" {
		t.Fatalf("catalog model default = %q, want Unknown", got)
	}
	c.CatalogDefaultManufacturer = "Acme"
	c.CatalogDefaultModel = "Cam-X"
	if c.CatalogManufacturer() != "Acme" || c.CatalogModel() != "Cam-X" {
		t.Fatal("catalog identity overrides not honored")
	}
}

// TestInviteTimeoutConfigurable pins the INVITE answer timeout knob.
func TestInviteTimeoutConfigurable(t *testing.T) {
	if got := (sip.Config{}).InviteTimeout(); got != 32*time.Second {
		t.Fatalf("default invite timeout = %v, want 32s", got)
	}
	if got := (sip.Config{InviteResponseTimeout: "5s"}).InviteTimeout(); got != 5*time.Second {
		t.Fatalf("invite timeout override = %v, want 5s", got)
	}
	if got := (sip.Config{InviteResponseTimeout: "garbage"}).InviteTimeout(); got != 32*time.Second {
		t.Fatalf("invalid invite timeout must fall back to 32s, got %v", got)
	}
}
