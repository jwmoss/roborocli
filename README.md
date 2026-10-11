# roborocli

[![Release](https://img.shields.io/github/v/release/jwmoss/roborocli)](https://github.com/jwmoss/roborocli/releases/latest)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

Command-line client for Roborock robot vacuums. It signs in to the Roborock
cloud, lists your vacuums, reads status and settings, and sends control
commands over the device's local TCP protocol or the cloud MQTT broker.

## Status

The protocol layer is reverse engineered and implemented from two sources:

- The Roborock Android app `4.74.08` (`com.roborock.smart`), decompiled with
  [REA](https://github.com/morluto/rea) and jadx. See
  [docs/protocol.md](docs/protocol.md) for the observed evidence.
- `python-roborock`, the library behind the Home Assistant integration, for
  the AES key schedules and the RPC payload shape that the app performs in a
  native library.

Supported vacuums are protocol `1.0` devices (S4 through G20S/Q10 era
hardware). Protocol `A01`, `B01`, and `L01` devices speak a different
message layout; the CLI negotiates `1.0` and `L01` on the local link.

## Install

```bash
go install github.com/jwmoss/roborocli/cmd/roborocli@latest
```

From source:

```bash
git clone https://github.com/jwmoss/roborocli.git
cd roborocli
make build
./bin/roborocli version
```

## Login

Roborock uses an email code, not a password.

```bash
roborocli login --email you@example.com
```

The CLI requests a code, prompts for it, and stores the account credentials
in `~/.config/roborocli/config.yaml` with mode `0600`. Use
`--send-code-only` to send the code without prompting.

## Device selection

```bash
roborocli devices                 # list vacuums with duid, model, and IP
roborocli status                  # uses the only device, or the configured one
roborocli --device <duid> status  # select explicitly
```

Set `default_duid` in the config file to avoid passing `--device`.
Set `default_host` to the vacuum IP to avoid passing `--host`.

The CLI caches the device list, with each device's local key, in the
config file. Device commands read the cache, so they make no cloud calls on
the local transport. The Roborock cloud rate limits its API. Run
`roborocli devices` to refresh the cache after you add, rename, or re-pair a
vacuum.

## Transport

The CLI prefers the local TCP connection on port `58867` and falls back to
the cloud MQTT broker:

```bash
roborocli status                  # local first, MQTT fallback
roborocli --host 192.168.1.50 status   # force a local IP
roborocli --mqtt status           # force the cloud broker
```

The local IP is discovered with `get_network_info` over MQTT and then cached
in the config file. Pass `--host` to skip discovery.

## Inspection

```bash
roborocli status                  # state, battery, fan, error, clean totals
roborocli info                    # device, network, consumables, rooms, history
roborocli info consumables        # brush, filter, and dock part lifetimes
roborocli info rooms              # room ids and names from the device map
roborocli info network            # IP, SSID, MAC, RSSI
roborocli info history            # cleaning summary
roborocli settings                # child lock, LED, carpet, volume, DND, map state
roborocli control fan             # read the fan speed
roborocli control water           # read the water box mode
roborocli control dnd             # read the do-not-disturb timer
```

## Control

```bash
roborocli control start           # start or resume cleaning
roborocli control pause
roborocli control stop
roborocli control charge          # return to the dock
roborocli control spot            # spot clean
roborocli control find            # locate the vacuum
roborocli control rooms 16 17     # clean specific rooms by id
roborocli control rooms kitchen "dining room" --repeat 2  # or by name
roborocli control goto 2300 -1200 # drive to a map coordinate in millimeters
roborocli control fan 103         # set the fan speed
roborocli control water 203       # set the water box mode
roborocli control volume 50       # set the speaker volume
roborocli control dnd set 22:30 07:00
roborocli control dnd off
roborocli control reset-consumable main_brush
```

Mutating commands are refused under `--dry-run`.

## Any other RPC method

Firmware features that the CLI does not wrap are reachable with `command`:

```bash
roborocli command get_map_status
roborocli command app_start '{"clean_type":3}' --json
roborocli command get_multi_maps_list
```

`command` refuses methods that change device state under `--dry-run`, unless
you pass `--force`.

## Cloud API

Signed requests to the Roborock rriot API use the same Hawk authentication as
the app:

```bash
roborocli raw GET /user/homes/12345
roborocli raw GET /ota/firmware/<duid>/updatev2 --query lang=en
roborocli raw POST /user/devices/<duid>/jobs --body '{"name":"cleanup"}'
```

Response bodies pass through unchanged, so large numbers keep their exact
value in `--json` output.

## Diagnostics

```bash
roborocli doctor                  # gateway, credentials, device reachability
roborocli doctor --json
roborocli whoami                  # stored account and region
roborocli config show             # effective config with secrets redacted
roborocli --trace-http status     # log cloud requests to stderr
```

## Configuration

`~/.config/roborocli/config.yaml`:

```yaml
base_url: https://usiot.roborock.com
username: you@example.com
client_id: <stable install id>
user_data:      # written by roborocli login
  rriot:
    u: ...
    s: ...
    h: ...
    k: ...
    r:
      a: https://api-us.roborock.com
      m: ssl://mqtt-roborock.com:8883
default_duid: abc123
default_host: 192.168.1.50
device_ips:
  abc123: 192.168.1.50
```

Environment overrides:

| Variable | Purpose |
| --- | --- |
| `ROBOROCLI_EMAIL` | account email |
| `ROBOROCLI_BASE_URL` | login gateway override |
| `ROBOROCLI_DUID` | default device |
| `ROBOROCLI_HOST` | vacuum IP used when `--host` is omitted |

## Development

```bash
make check      # go mod tidy -diff, go vet, tests, build
make build      # build ./bin/roborocli
```

The protocol package has golden-vector tests for the timestamp scramble and
the V1/L01 key schedules, plus an end-to-end test that runs the local
handshake and RPC exchange against an in-process device simulator.

## License

MIT