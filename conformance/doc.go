// Package conformance hosts the device↔platform loopback test suite.
//
// The library carries both GB/T 28181 roles — device (UAC, package device)
// and platform (UAS, package platform + platform/sip). This suite wires a
// real device.Server against a real platform SIP server on localhost and
// drives the full interplay: REGISTER with digest auth, keepalive liveness,
// catalog discovery, INVITE-driven live streaming with a byte-level
// RTP/PS round-trip (device psmux push → platform reassembly → psdemux), and
// BYE teardown.
//
// Its value over single-role tests (fake SIP sockets on one side): UAC and
// UAS implementations are forced to agree on the same protocol reading —
// field encodings, branch/CSeq discipline, SDP shapes — inside one
// repository, on every CI run.
//
// The package intentionally contains no production code; it exists so the
// suite can import both roles without creating an import cycle.
package conformance
