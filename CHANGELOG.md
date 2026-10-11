# Changelog

## Unreleased

## [1.1.0] - 2026-10-10

- Add detailed cleaning records with completion, interruption, source, and error evidence.
- Add model capabilities, named cleaning modes, and verified setting changes.
- Add dock washing, drying, collection, and saved app routines.
- Add continuous JSON-line events and periodic live snapshots.
- Add native Go map download, room geometry, PNG output, and V1 floor selection.
- Add native A01 Dyad/Zeo and B01 Q7/Q10 MQTT codecs, data points, and family commands.
- Fix MQTT frame CRCs and decode JSON-string RPC replies. Reject firmware error results.
- Redact device local keys from inspection output.
- Use a Go live runner with read-only checks and mutation guards.

V1 reads have live verification. Physical actions and A01/B01 hardware remain unverified.
Saved record maps and floor selection remain V1-only.

## [1.0.3] - 2026-10-10

- Apply the configured timeout to cloud requests.
- Preserve exact RPC JSON numbers and prevent force from bypassing dry-run.
- Replace synthetic suites with real device checks and static CI.

## 1.0.2

- Read the `[id, name_id, tag]` room mapping form in `info rooms`, and name
  rooms from the cloud home data. Refresh the cache when a name is missing.
- Accept room names in `control rooms`, and add `--repeat`.
- Send `app_segment_clean` as `[{"segments": [...], "repeat": n}]`.
- Increase the local request seq on each call. The device dropped the
  connection after a lower random seq, so multi-call commands such as `info`,
  `settings`, and `control rooms` lost results.

## 1.0.1

- Show clean area in square meters. Firmware reports mm2, not cm2.
- Read the short `clean_*` keys in `info history`.
- Accept the array form of `get_consumable` in `info consumables`.
- Show `_times` consumable values as counts, not durations.
- Cache the home payload in the config file. Device commands no longer call
  the cloud on the local transport; `roborocli devices` refreshes the cache.
- Use the gateway stored at login, and skip the rate-limited `getUrlByEmail`.
- End each stderr diagnostic with a newline.
- Fix a data race between `LocalClient.Close` and the read loop.

## 1.0.0

- Add a Roborock client for account login, device listing, and vacuum control.
- Read full status over the local L01 socket, and fall back to the cloud shadow.
- Store a default vacuum address in `default_host`.
- Build with Go 1.27.2 so the release audit passes.
