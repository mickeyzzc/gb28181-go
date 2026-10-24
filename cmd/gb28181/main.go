// Command gb28181 is the all-in-one interop CLI for the gb28181-go
// library: a device-side (UAC) emulator, a minimal platform-side (UAS)
// server, and a media-path self-check — one subcommand each. Every
// subcommand file doubles as a runnable integration sample; the flags
// are the whole configuration surface (the old prebuilt examples
// hardcoded theirs, which made the shipped binaries unusable).
//
// Usage:
//
//	gb28181 device-register -platform 192.168.1.10:5060 -password secret
//	gb28181 platform-uas -listen :5060 -password secret
//	gb28181 psmux-rtp
package main

import (
	"fmt"
	"os"
)

var usageText = `gb28181 — interop CLI for gb28181-go

Subcommands:

  device-register   Emulate a GB/T 28181 camera (UAC): register, keepalive,
                    catalog answers, RTP/PS streaming once INVITEd.
  platform-uas      Minimal GB/T 28181 platform (UAS) for devices to
                    register into: digest auth, catalog, INVITE streaming.
  psmux-rtp         Media-path self-check: H.264 → MPEG-PS → RTP loopback
                    with packet statistics.

Run "gb28181 <subcommand> -help" for its flags.
`

func main() {
	os.Exit(dispatch(os.Args[1:]))
}

// dispatch dispatches one subcommand; the exit code follows the usual
// CLI convention (0 success, 2 usage error). Split from main so the
// dispatch table itself is testable.
func dispatch(args []string) int {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, usageText)
		return 2
	}
	sub, rest := args[0], args[1:]
	var fn func([]string)
	switch sub {
	case "device-register":
		fn = runDeviceRegister
	case "platform-uas":
		fn = runPlatformUAS
	case "psmux-rtp":
		fn = runPsmuxRTP
	case "help", "-h", "-help", "--help":
		fmt.Print(usageText)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "gb28181: unknown subcommand %q\n\n", sub)
		fmt.Fprint(os.Stderr, usageText)
		return 2
	}
	fn(rest)
	return 0
}
