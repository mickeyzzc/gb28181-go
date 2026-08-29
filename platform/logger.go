package platform

import "log/slog"

// logger is the package-level logger shared by the platform components.
// Hosts can re-target it via slog.SetDefault before starting a server.
var logger = slog.Default().With("component", "gb28181-platform")
