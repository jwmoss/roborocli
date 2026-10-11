# Roborock protocol notes

This file records how `roborocli` implements the Roborock protocols and which
observations support each decision. It exists so a future change can be checked
against evidence instead of guesswork.

## Evidence sources

1. **Roborock Android app 4.74.08** (`com.roborock.smart`, version code
   100936), 29,029 classes. Inspected with
   [REA](https://github.com/morluto/rea) for package identity and with jadx
   1.5.6 for code. Evidence ID for the package inspection:
   `ev_0f010c64ce0fbc79c359baa57c0351ad3049073b198982f323822e6a26cfa85a`.
2. Adapted wire-format definitions, feature tables, history codes, and map layouts.
   See [third-party notices](../THIRD_PARTY_NOTICES.md) for the applicable license.
3. Live device responses and the checks recorded below.

All runtime implementations and the live test runner use Go.

REA class search returned `JADX class inventory is duplicated or differs from
the loaded class count` on this artifact, so the class-level work used the
jadx CLI directly. REA still provided the package-level evidence.

## Message framing

Observed in `p238o0O000o/OooO0o.java` (the app's `MqttWireFrame`):

```text
version(3) | seq(4) | random(4) | timestamp(4) | protocol(2) | [len(2) | payload] | crc32(4)
```

- All integers are big-endian.
- The CRC32 covers every byte before it. The app checks
  `crc32(buf[0:len-4]) == be32(buf[len-4:])`.
- A payload of zero length means no payload. The app rejects payloads shorter
  than 23 bytes and versions outside `1.0`, `A01`, and `B01`.
- The device key must be exactly 16 characters; the app raises
  `PAYLOAD_BAD_KEY_ERROR` otherwise.
- MQTT frames have no length prefix. Local TCP frames add a 4-byte big-endian
  length that counts the header, payload, and CRC.
- Payload-less handshake frames end after the 17-byte header, which is how
  `roborocli` encodes hello messages and how its decoder treats a frame with
  no trailing bytes.

Protocol codes: `0/1` hello, `2/3` ping, `4/5` general request and response,
`101/102` RPC request and response, `301` map response.

## Payload encryption

`roborocli` implements these schedules:

- **1.0**: AES-128-ECB with a PKCS#7 padded payload. The key is
  `md5(encodeTimestamp(timestamp) + localKey + "TXdfu$jyZ#TZHsg4")`.
- **L01**: AES-256-GCM. The key is
  `sha256(encodeTimestamp(timestamp) + localKey + salt)`, the nonce is
  `sha256(seq || nonce || timestamp)[:12]`, and the additional authenticated
  data is
  `seq || connectNonce || [ackNonce] || nonce || timestamp`.
- **A01**: AES-CBC with the 16-byte device local key. The IV is the ASCII
  substring `[8:24]` of `md5(hex8(random) + "726f626f726f636b2d67a6d6da")`.
- **B01**: AES-CBC with the same key source. The IV uses substring `[9:25]`
  of `md5(hex8(random) + "5wwh9ikChRjASpMU8cxg7o1d2E")`.
  Both CBC schedules use PKCS#7 padding. Q7 also pads its JSON before frame encryption.

`encodeTimestamp` renders the unix timestamp as 8 hex digits and permutes them
through the index order `5,6,3,7,1,2,0,4`.

The app calls `RRCodecApi.codec(...)`, a native method. The key schedule is not
visible in the decompiled Java. Live checks verify the V1 and L01 schedules;
A01 and B01 schedules still require hardware verification.

The decoder skips garbage before a frame by scanning for a version header, the
same approach the reference parser uses. This tolerates the local length
prefix, which never matches a version string.

## Local transport

`com/roborock/internal/android/service/sock/OooO00o.java` builds the device
URL as `tcp://<ip>:58867`, and logs under the name `LocalClientService`.

Handshake sequence:

1. The client sends a length-prefixed `L01` CONNECT frame. The frame carries a
   random connect nonce and a keepalive of 10 seconds.
2. The device answers with a CONNACK frame. Bytes 7-10 of that body are the
   ack nonce.
3. The client sends each RPC as a PUBLISH frame (type 4). L01 encryption uses
   both nonces.
4. If the device does not answer CONNECT, the client closes the socket. It
   then sends the classic hello for version `1.0`.

Current firmware answers CONNECT on port 58867 even when the cloud protocol
field says `1.0`.

## RPC payload

Observed in `PublishBean1_0` and the RPC send path: data points are a map of
string keys to string values, and each value is itself JSON text.

```json
{"dps": {"101": "{\"id\":123,\"method\":\"get_status\",\"params\":[]}"},"t":1770000000}
```

Responses use data point `102` and carry `id`, `result`, and optionally
`error`. A device that does not know a method answers with the string
`unknown_method`.

The decoder accepts both object and JSON-string data point values. It treats
an RPC `error`, missing result, null result, or a result string other than `ok`
as a failure. It preserves raw JSON numbers. MQTT frames include the trailing CRC32.
Before the CRC and string-decoding fixes, the same live MQTT status request timed out.
Before result validation, `get_airdry_hours` returned `unknown_method` with exit code zero.

## MQTT

`com/roborock/internal/common/mqtt/OooO.java` builds the publish topic as:

```text
rr/m/i/<u>/<md5(u:k)[2:10]>/<duid>
```

and `com/facebook/imagepipeline/request/OooO0O0.java` defines the hash as the
hex digest of `u + ":" + k`, sliced from index 2 to 10. The broker
credentials are the same value as the username and `md5(s:k)[16:]` as the
password.

## Hawk authentication

`com/roborock/smart/refactor/network/OooO.java` is an OkHttp interceptor that
signs rriot API requests. The mac is HMAC-SHA256 keyed by `h` over the colon
joined string:

```text
u : s : nonce : ts : md5(path) : md5(sortedQuery) : md5(payload)
```

- `path` is the request path rebuilt from its URL segments with a leading `/`.
- The query and form slots are the md5 of the sorted `k=v&...` string.
- The payload slot is the md5 of the raw JSON body for JSON requests.

The header is:

```text
Authorization: Hawk id="<u>", s="<s>", ts="<ts>", nonce="<nonce>", mac="<mac>"
X-IOTSDK-VERSION: 1.0.1
```

## Cloud endpoints

Observed in `com/roborock/internal/common/network/api/IotApi.java` and
`com/roborock/smart/sdk/networkv2/api/IAuthApi.java`:

| Purpose | Endpoint |
| --- | --- |
| sign a login challenge | `POST /api/v3/key/sign?s=` |
| home detail | `GET /api/v1/getHomeDetail` |
| home payload | `GET /v4/user/homes/{homeId}` |
| rooms | `GET /user/homes/{homeId}/rooms` |
| device detail | `GET /user/devices/{duid}` |
| cloud command | `POST /devices/{duid}/command` |
| cloud setting | `POST /devices/{duid}/setting` |
| device shadow | `GET /devices/{duid}/shadow` |
| schedules | `GET /user/devices/{duid}/jobs` |
| firmware | `GET /ota/firmware/{duid}/updatev2` |
| saved routines | `GET /user/scene/device/{duid}` |
| execute routine | `POST /user/scene/{id}/execute` |

The v4 email login endpoints are not present in the decompiled Java; they are
reached through obfuscated or native code. `roborocli` uses paths verified
against the live service.

## History and capabilities

V1 summaries expose record IDs in either `records` or array element 3.
Record IDs are start timestamps. Sort them descending before selecting the latest.
Records can be objects, split-record object lists, or legacy arrays. For split
records, sum duration and area, retain the first start source, and use the final
record's completion evidence. Legacy arrays do not prove completion.
`finish_reason: 60` means manual interruption; it does not identify who sent the command.

The embedded reference table combines model hardware flags with `feature_info`,
`feature_info_ex`, `new_feature_info`, and `new_feature_info_str` rules.
Use arbitrary-width integers for masks. The string variant uses hexadecimal.
The active dock type controls wash, dry, and dust-collection support.
Named mode writes use `set_clean_motor_mode`, or `set_custom_mode` on dry-only V1 models.
Read settings back before reporting verification.

## Events

Both transports expose unsolicited data points. V1 RPC replies remain on the
request channel. `watch` combines these pushes with periodic `get_status` snapshots.
The event queue holds 256 messages. When full, it counts losses and reports the
count on the next delivered event. A disconnected stream returns an error.
Snapshots describe current state; they do not provide a historical command audit.

## Map envelopes and geometry

V1 calls `get_map_v1` or `get_clean_record_map` with a random 16-byte security nonce.
Protocol 301 replies carry a 24-byte envelope. Match its endpoint and the
little-endian request ID at bytes 16-17. Decrypt the body with AES-CBC, the nonce
as key, and a zero IV. Remove PKCS#7 padding, then decompress gzip.
An ordinary RPC acknowledgement does not complete a pending map request.

RR maps contain a header and little-endian typed blocks. The decoder reads the
floor image, charger, robot, path, walls, and restricted areas. Image pixels
cover 50 mm. Room IDs occupy the upper five bits of floor pixels.
The PNG renderer uses Go's image and PNG packages. It draws floor pixels,
room colors, available paths, and position markers. Geometry output also
retains walls and restricted areas.

Q7 map requests use `service.get_map_list` and `service.upload_by_mapid`.
The protocol 301 body has base64 AES-ECB ciphertext. The map key derives from
the device serial and model suffix. Decrypted hex contains zlib-compressed
SCMap protobuf bytes. The Go decoder reads header, grid, poses, room outlines,
and room names. It supports protobuf wire types 0, 1, 2, and 5 without a new dependency.

Q10 current maps arrive as protocol 301 packets with marker `01 01`.
`REQUEST_DPS` (102) with `{}` requests a safe current status/map push.
Do not use `dpMultiMap op:get` for a read: some firmware starts relocation or cleaning.
The decoder checks dimensions, expands raw LZ4, and reads the room records.
Coordinates use the documented 5 mm offsets and a 25,500 mm common origin.
All decoders bound expansion and dimensions before image allocation.

## A01 and B01 command families

The home payload `pv` and product model select the family. A01 wet/dry vacuums
use Dyad codes. A01 washing machines and dryers use Zeo codes. Unknown models fail
explicitly. These families use MQTT; the CLI does not guess a local V1 connection.
Queries send data point 10000 with the JSON-encoded list of requested codes.
Reads merge responses until all requested codes arrive or the timeout expires.

B01 model suffixes containing `sc` use Q7 property/service RPCs. Requests carry
`method`, string `msgId`, and `params` under data point 10000. Match the reply ID
and require response code zero. `service.get_record_list` returns totals and
JSON-string record details. Q7 area units require division by 100 for square meters.
Unconfirmed Q7 task, source, and fault codes remain raw instead of borrowing V1 labels.

B01 suffixes containing `ss` use Q10 data points. The decoder unwraps nested
`dpCommon` (101) replies. History requests use common point 52 with `op:list`.
Each history string has twelve fields, including start time, minutes, square
meters, outcome, and start source. Codes 0/1/2 mean interrupted/completed/stopped.

Generic data point writes return a broker publication receipt, not a device
acknowledgement. Named mode commands perform a follow-up read. Room cleaning uses
Q7 `service.set_room_clean` or Q10 point 201 with the firmware's `clean_paramters` key.

## Live evidence and remaining limits

Read-only verification uses a QX Revo Plus (`roborock.vacuum.a168`), firmware 02.49.24.
The live suite checks local and forced-MQTT status, error rejection, detailed
history, capabilities, dock reads, saved routines, map geometry, PNG decoding,
watch snapshots, and dry-run mutation guards. A rendered live floor map also
received a visual check. Device keys are redacted from `devices` and `info` output.

Physical controls and A01/B01 hardware have no live verification in this workspace.
Idle-device snapshots do not verify unsolicited push delivery. V1 supports record
maps and floor selection; B01 currently supports current-map reads and basic rendering.
Obstacle photos, complete app overlays, and B01 saved-map selection remain outside
the implemented command surface. Firmware-specific V1 methods and Q7 services
remain accessible through `command`; A01/Q10 features use `datapoints`.
