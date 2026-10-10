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
2. **python-roborock**, the library behind the Home Assistant integration,
   used for the AES key schedules and RPC payload semantics that the app
   performs inside a native library (`RRCodecApi`).

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

`roborocli` implements two schedules:

- **1.0**: AES-128-ECB with a PKCS#7 padded payload. The key is
  `md5(encodeTimestamp(timestamp) + localKey + "TXdfu$jyZ#TZHsg4")`.
- **L01**: AES-256-GCM. The key is
  `sha256(encodeTimestamp(timestamp) + localKey + salt)`, the nonce is
  `sha256(seq || nonce || timestamp)[:12]`, and the additional authenticated
  data is
  `seq || connectNonce || [ackNonce] || nonce || timestamp`.

`encodeTimestamp` renders the unix timestamp as 8 hex digits and permutes them
through the index order `5,6,3,7,1,2,0,4`.

Evidence for these two schedules is python-roborock, which has run them in the
Home Assistant integration against real devices. The app calls into
`RRCodecApi.codec(...)`, a native method, so the key schedule itself is not
visible in the decompiled Java.

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

The v4 email login endpoints are not present in the decompiled Java; they are
reached through obfuscated or native code. `roborocli` uses the paths from
python-roborock, which signs in successfully against the live service.

## Known gaps

- Protocol `A01` and `B01` devices use a different layout and are not
  supported. They announce their version in the home data `pv` field.
- Maps and photos are binary responses that require the blob security
  envelope. `roborocli` reads rooms and history instead of rendering maps.
- Firmware-specific commands vary by model. `roborocli command <method>`
  reaches anything the device accepts, including methods with no wrapper.