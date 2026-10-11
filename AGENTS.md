# AGENTS.md

## Purpose

`roborocli` is a Go CLI that controls Roborock robot vacuums over the cloud
REST API, the cloud MQTT broker, and the device local TCP protocol on port
58867. Keep provider-specific behavior in commands and typed packages; keep
generic transport, config, and output helpers small and reusable.

`docs/protocol.md` records the observed protocol details and their evidence.
Change it when the protocol implementation changes.

## CLI Rules

- Primary command output goes to stdout.
- Errors, traces, and diagnostics go to stderr.
- Keep `--json` stable for scripts.
- Keep `--plain` stable and line-oriented where implemented.
- Do not print cloud credentials. `rriot.k`, `rriot.h`, and `rriot.s` are
  secrets; show only redacted status.
- Do not add credential command-line flags. Pass credentials through the
  config file or the environment.
- Guard device mutations and local config or credential writes with
  `--dry-run`.
- Keep raw JSON numbers exact. Use `json.RawMessage` for pass-through
  payloads; do not round trip bodies through `any`.
- Prefer the local transport and fall back to MQTT, and say which transport
  served the command.
- Commands that need credentials should explain the missing value in the
  error.

## Protocol Rules

- Treat `internal/roborock` as the only place that knows the wire format.
- Confirm a protocol change against `docs/protocol.md` or a decompiled app
  artifact before committing to it.

## Validation

Run before handoff:

```bash
make check
```

For release config changes, use the pinned release tool:

```bash
make release-check
```

## Test policy

- Keep only live end-to-end tests against the configured service and static checks.
- Do not add unit tests, mock transports, fake servers, device simulators, or synthetic subprocess fixtures.
- Run `make check` for formatting, module consistency, vet, script syntax, and builds.
- Run `make test` with existing credentials for read-only live verification.
- Report missing credentials or service failures as blocked live checks, never passes.
- CI runs static checks without credentials. It does not run live service tests.
