# Security Policy

## Supported versions

Only the latest tagged release and the current `main` branch receive
security fixes. Older tags are end-of-life — the module is consumed via
`mickeyzzc/gb28181-go`, upgrade is a version bump or git-pin update.

| Version | Supported |
|---------|-----------|
| latest tag | ✅ |
| `main` | ✅ (fixes land here first, PR-only) |
| older tags | ❌ end-of-life |

## Reporting a vulnerability

**Please do not open a public issue for security problems.**

- Prefer a private [GitHub security advisory](https://github.com/mickeyzzc/gb28181-go/security/advisories/new).
- Alternatively email the maintainer (see the GitHub profile); include
  `gb28181-go security` in the subject.

Include reproduction details (SIP trace, MANSCDP body, capture) when
possible. You will get an acknowledgement within 7 days. Urgent fixes
are released as patch versions out of band; otherwise they ship with the
next capability package (merge ≠ release — see `CONTRIBUTING.md`).

## Scope

Security-relevant surfaces maintained by this library:

- SIP message parsing from **untrusted** platforms/peers (UDP and the
  bounded TCP/SIPS readers, `MaxSIPMessageSize`), REGISTER handling, and
  the platform-side brute-force lockout.
- MANSCDP XML codec (GB2312/GBK/GB18030/UTF-8 transcoding).
- RTP/PS muxing and demuxing of media payloads; SDP generation/parsing.
- `security35114` (build tag `gb35114`): SM2 certificate authentication
  material handling — certificates and keys are supplied by the caller
  and never logged.
- Platform (UAS) surfaces: SIP server core, cascade client, challenge/
  verify state machine, Note verification.

Out of scope: consumers' credential storage, SIP-over-TLS deployment
choices, media encryption beyond GB 35114 level A (levels B/C need SVAC
hardware and are not implemented).

## Safe harbor

Fuzzing and penetration testing against your own deployments, and
submitting crashers found by `go test -fuzz`, are welcome — please still
report anything that survives the in-repo fuzz corpus privately first.
