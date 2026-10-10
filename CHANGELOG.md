# Changelog

## Unreleased

- Show clean area in square meters. Firmware reports mm2, not cm2.
- Read the short `clean_*` keys in `info history`.
- Accept the array form of `get_consumable` in `info consumables`.
- Show `_times` consumable values as counts, not durations.

## 1.0.0

- Add a Roborock client for account login, device listing, and vacuum control.
- Read full status over the local L01 socket, and fall back to the cloud shadow.
- Store a default vacuum address in `default_host`.
- Build with Go 1.27.2 so the release audit passes.
