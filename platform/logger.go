package platform

import "log/slog"

// logger returns the logger used by the platform components, derived from
// the CURRENT slog default on every call — hosts may retarget logging at
// any time via slog.SetDefault and this package follows.
func logger() *slog.Logger {
	return slog.Default().With("component", "gb28181-platform")
}
