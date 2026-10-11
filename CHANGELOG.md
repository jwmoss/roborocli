# Changelog

## Unreleased

- Read the `[id, name_id, tag]` room mapping form in `info rooms`, and name
  rooms from the cloud home data. Refresh the cache when a name is missing.
- Accept room names in `control rooms`, and add `--repeat`.
- Send `app_segment_clean` as `[{"segments": [...], "repeat": n}]`, as
  python-roborock and Home Assistant do.

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
