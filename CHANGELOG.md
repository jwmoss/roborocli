# Changelog

## Unreleased

## 1.0.0

- Add a Roborock client for account login, device listing, and vacuum control.
- Read full status over the local L01 socket, and fall back to the cloud shadow.
- Store a default vacuum address in `default_host`.
- Build with Go 1.27.2 so the release audit passes.
