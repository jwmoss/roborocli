# roborocli

[![Release](https://img.shields.io/github/v/release/jwmoss/roborocli)](https://github.com/jwmoss/roborocli/releases/latest)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

Command-line client for Roborock robot vacuums. It signs in to the Roborock
cloud, lists your vacuums, reads status and settings, and sends control
commands over the device's local TCP protocol or the cloud MQTT broker.

## Status

The CLI, map decoders, PNG renderer, and live test runner use native Go.
See [docs/protocol.md](docs/protocol.md) for protocol details and live evidence.

| Family | Transport | Implemented features |
| --- | --- | --- |
| V1 vacuum | Local `1.0`/`L01`, MQTT | Status, controls, detailed history, capabilities, dock settings, maps, routines, watch |
| B01 Q7 | MQTT | Properties/services, basic and room controls, named modes, history, current map and rooms, watch |
| B01 Q10 | MQTT | Data points, basic and room controls, named modes, history, current map and rooms, watch |
| A01 Dyad/Zeo | MQTT | Status, named/numeric data point reads and writes, watch |

Live verification covers a QX Revo Plus (`roborock.vacuum.a168`). A01 and
B01 implementations follow the reference source; hardware verification remains open.

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
roborocli history last            # latest record, with completion evidence
roborocli history records --limit 10
roborocli history record 1791684530
roborocli capabilities            # supported modes, features, dock capabilities
roborocli watch --duration 1m      # pushes plus periodic snapshots, as JSON lines
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

Named modes use the device capabilities and verify settings after the write:

```bash
roborocli control mode vacuum --fan max
roborocli control mode mop --water medium
roborocli control mode vac_and_mop --fan balanced
```

Mode changes set options. Use `control start` or `control rooms` to start a task.
An acknowledgement or a charging state does not prove that a task completed.
Check `history last` for the outcome, finish reason, start source, and errors.
JSON records retain the original device fields in `parts`.
Legacy V1 records and unknown Q7 status codes retain an `unknown` outcome.
B01 history IDs use start timestamps; Q10 also retains its native `record_key`.

## Dock and saved routines

```bash
roborocli dock status
roborocli dock wash-start          # wash-stop, dry-start, dry-stop, empty-start, empty-stop
roborocli dock auto-dry on         # omit the value to read it
roborocli dock wash-mode balanced
roborocli dock empty-mode smart
roborocli routines list
roborocli routines run "Vac followed by Mop"
```

Dock wrappers require supported V1 dock features. Settings read back their value.
Dock actions report acknowledgement; check `dock status` for the resulting state.
Routine execution reports cloud submission. Check live status and history for its outcome.

## Maps

```bash
roborocli maps list
roborocli maps show               # geometry, rooms, robot, charger, and available path
roborocli maps download --output floor.bin
roborocli maps render --output floor.png
roborocli maps render --record 1791684530 --output previous.png
roborocli maps use 0              # V1 saved map flag; refuses an active task
```

Maps use MQTT. File output refuses an existing path and creates files with mode `0600`.
Use `--output -` for binary stdout. `--dry-run` permits stdout but refuses file writes.
Record maps and map selection currently require V1. B01 rendering covers the current
floor and room geometry; it does not compose every trace, obstacle, or app overlay.

## Other device families

```bash
roborocli --device <duid> datapoints list
roborocli --device <duid> datapoints get <name-or-code>
roborocli --device <duid> datapoints set <name-or-code> '<json-value>'
roborocli --device <q7-duid> command prop.get '{"property":["status","quantity"]}'
```

A01 and Q10 data point writes report `published: true, verified: false`.
Use `datapoints get` to check the value. Unknown codes remain available as numbers.
These families use their own codes; V1 state names do not apply to them.
Use `datapoints list` or native Q7 methods for features without a command wrapper.

## Any other RPC method

Firmware features that the CLI does not wrap are reachable with `command`:

```bash
roborocli command get_map_status
roborocli command app_start '{"clean_type":3}' --json
roborocli command get_multi_maps_list
```

`command` refuses methods that change device state under `--dry-run`, including when `--force` is present.
RPC JSON parameters and results preserve exact numeric values.
`--timeout` applies to cloud requests and device transports.
Firmware errors such as `unknown_method` return a nonzero exit code.

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
make check      # static checks and build
make test       # live, read-only V1 verification with existing credentials
```

The Go live runner calls the compiled CLI. It checks status over both transports,
history, capabilities, maps and PNG output, routines, dock reads, watch snapshots,
and mutation guards. It uses `--dry-run` to prevent cache writes and device mutations.
It does not test physical controls or A01/B01 hardware. Idle-device checks do not
prove unsolicited push delivery. The suite requires a configured V1 vacuum with a map.
CI runs static checks without credentials. Unit tests and synthetic fixtures are not part of this project.

## License

MIT. See [LICENSE](LICENSE).
