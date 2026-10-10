// Package roborock implements the Roborock device protocols: the local TCP
// protocol on port 58867, the MQTT control protocol, and the cloud REST API.
//
// The wire format and crypto derive from two sources:
//   - The Roborock Android app 4.74.08 (com.roborock.smart), decompiled with
//     REA/jadx: MqttWireFrame (version, seq, random, timestamp, protocol,
//     payload length, payload, CRC32), LocalClientService (tcp://host:58867),
//     and the Hawk auth interceptor.
//   - python-roborock (the library behind the Home Assistant integration),
//     which documents the AES key derivation for protocol versions 1.0 and
//     L01 and the full RPC payload shape.
package roborock

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	mrand "math/rand"
	"strings"
)

// Salt is the fixed salt used in the 1.0/L01 key derivation.
const Salt = "TXdfu$jyZ#TZHsg4"

// Protocol version identifiers sent in the first three frame bytes.
const (
	VersionV1  = "1.0"
	VersionL01 = "L01"
)

// Message protocol codes carried in the frame header.
const (
	ProtoHelloRequest    = 0
	ProtoHelloResponse   = 1
	ProtoPingRequest     = 2
	ProtoPingResponse    = 3
	ProtoGeneralRequest  = 4
	ProtoGeneralResponse = 5
	ProtoRPCRequest      = 101
	ProtoRPCResponse     = 102
	ProtoMapResponse     = 301
)

// validVersions lists protocol version byte sequences accepted in a frame.
var validVersions = [][]byte{[]byte(VersionV1), []byte("A01"), []byte("B01"), []byte(VersionL01)}

// Message is one decoded Roborock wire message.
type Message struct {
	Version   string // "1.0", "A01", "B01", or "L01"
	Seq       uint32
	Random    uint32
	Timestamp int32
	Protocol  uint16
	Payload   []byte // decrypted payload; nil when absent
}

// codecKeys carries the negotiated session values needed to decrypt and
// encrypt L01 payloads. V1 payloads need only the local key.
type codecKeys struct {
	localKey     string
	connectNonce uint32
	ackNonce     uint32 // zero when not yet acknowledged
}

// encodeTimestamp scrambles a unix timestamp the way Roborock devices do:
// hex digits of the timestamp, padded to 8, reordered by a fixed index map.
func encodeTimestamp(ts int32) string {
	value := fmt.Sprintf("%08x", uint32(ts))
	const order = "\x05\x06\x03\x07\x01\x02\x00\x04"
	var b strings.Builder
	for i := 0; i < len(order); i++ {
		b.WriteByte(value[order[i]])
	}
	return b.String()
}

// v1Key derives the 16-byte AES-128 key for protocol 1.0 payloads:
// md5(encodeTimestamp(ts) || localKey || salt).
func v1Key(ts int32, localKey string) []byte {
	sum := md5.Sum([]byte(encodeTimestamp(ts) + localKey + Salt))
	return sum[:]
}

// pkcs7Pad pads data to the AES block size.
func pkcs7Pad(data []byte, blockSize int) []byte {
	pad := blockSize - len(data)%blockSize
	return append(data, bytes.Repeat([]byte{byte(pad)}, pad)...)
}

// pkcs7Unpad removes PKCS#7 padding and reports invalid padding.
func pkcs7Unpad(data []byte, blockSize int) ([]byte, error) {
	if len(data) == 0 || len(data)%blockSize != 0 {
		return nil, errors.New("invalid padded length")
	}
	pad := int(data[len(data)-1])
	if pad == 0 || pad > blockSize || pad > len(data) {
		return nil, errors.New("invalid padding")
	}
	for _, b := range data[len(data)-pad:] {
		if int(b) != pad {
			return nil, errors.New("invalid padding")
		}
	}
	return data[:len(data)-pad], nil
}

// encryptV1 encrypts a payload with AES-128-ECB using the 1.0 key schedule.
func encryptV1(payload []byte, ts int32, localKey string) ([]byte, error) {
	block, err := aes.NewCipher(v1Key(ts, localKey))
	if err != nil {
		return nil, err
	}
	padded := pkcs7Pad(payload, aes.BlockSize)
	out := make([]byte, len(padded))
	for off := 0; off < len(padded); off += aes.BlockSize {
		block.Encrypt(out[off:off+aes.BlockSize], padded[off:off+aes.BlockSize])
	}
	return out, nil
}

// decryptV1 decrypts an AES-128-ECB payload produced by encryptV1.
func decryptV1(payload []byte, ts int32, localKey string) ([]byte, error) {
	block, err := aes.NewCipher(v1Key(ts, localKey))
	if err != nil {
		return nil, err
	}
	if len(payload)%aes.BlockSize != 0 || len(payload) == 0 {
		return nil, errors.New("ciphertext is not block aligned")
	}
	out := make([]byte, len(payload))
	for off := 0; off < len(payload); off += aes.BlockSize {
		block.Decrypt(out[off:off+aes.BlockSize], payload[off:off+aes.BlockSize])
	}
	return pkcs7Unpad(out, aes.BlockSize)
}

// l01Key derives the 32-byte AES-256 key for L01 payloads:
// sha256(encodeTimestamp(ts) || localKey || salt).
func l01Key(ts int32, localKey string) []byte {
	sum := sha256.Sum256([]byte(encodeTimestamp(ts) + localKey + Salt))
	return sum[:]
}

func be32(v uint32) []byte {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], v)
	return b[:]
}

// l01IV derives the 12-byte GCM nonce: sha256(seq||nonce||timestamp)[:12].
func l01IV(seq, nonce uint32, ts int32) []byte {
	sum := sha256.Sum256(append(append(be32(seq), be32(nonce)...), be32(uint32(ts))...))
	return sum[:12]
}

// l01AAD builds the additional authenticated data:
// seq||connectNonce||[ackNonce]||nonce||timestamp.
func l01AAD(seq, nonce, connectNonce, ackNonce uint32, ts int32) []byte {
	aad := append(be32(seq), be32(connectNonce)...)
	if ackNonce != 0 {
		aad = append(aad, be32(ackNonce)...)
	}
	aad = append(aad, be32(nonce)...)
	return append(aad, be32(uint32(ts))...)
}

// encryptL01 seals a payload with AES-256-GCM and appends the tag.
func encryptL01(payload []byte, keys codecKeys, m *Message) ([]byte, error) {
	block, err := aes.NewCipher(l01Key(m.Timestamp, keys.localKey))
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCMWithNonceSize(block, 12)
	if err != nil {
		return nil, err
	}
	return gcm.Seal(nil, l01IV(m.Seq, m.Random, m.Timestamp), payload,
		l01AAD(m.Seq, m.Random, keys.connectNonce, keys.ackNonce, m.Timestamp)), nil
}

// decryptL01 opens an AES-256-GCM payload produced by encryptL01.
func decryptL01(payload []byte, keys codecKeys, m *Message) ([]byte, error) {
	if len(payload) < 16 {
		return nil, errors.New("payload shorter than a GCM tag")
	}
	block, err := aes.NewCipher(l01Key(m.Timestamp, keys.localKey))
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCMWithNonceSize(block, 12)
	if err != nil {
		return nil, err
	}
	return gcm.Open(nil, l01IV(m.Seq, m.Random, m.Timestamp), payload,
		l01AAD(m.Seq, m.Random, keys.connectNonce, keys.ackNonce, m.Timestamp))
}

// encryptPayload encrypts the message payload for the message version.
func encryptPayload(m *Message, keys codecKeys) ([]byte, error) {
	switch m.Version {
	case VersionL01:
		return encryptL01(m.Payload, keys, m)
	case VersionV1, "A01":
		return encryptV1(m.Payload, m.Timestamp, keys.localKey)
	default:
		return nil, fmt.Errorf("unsupported protocol version %q", m.Version)
	}
}

// decryptPayload decrypts the message payload for the message version.
func decryptPayload(payload []byte, keys codecKeys, m *Message) ([]byte, error) {
	switch m.Version {
	case VersionL01:
		return decryptL01(payload, keys, m)
	case VersionV1, "A01":
		return decryptV1(payload, m.Timestamp, keys.localKey)
	default:
		return nil, fmt.Errorf("unsupported protocol version %q", m.Version)
	}
}

// EncodeLocalFrame serializes a message as a length-prefixed local TCP frame:
// length(4) || version(3) || seq(4) || random(4) || timestamp(4) ||
// protocol(2) || [len(2) || encrypted payload] || crc32(4).
// The CRC covers every frame byte after the length prefix.
func EncodeLocalFrame(m *Message, localKey string, connectNonce, ackNonce uint32) ([]byte, error) {
	keys := codecKeys{localKey: localKey, connectNonce: connectNonce, ackNonce: ackNonce}
	body, err := encodeBody(m, keys)
	if err != nil {
		return nil, err
	}
	body = binary.BigEndian.AppendUint32(body, crc32.ChecksumIEEE(body))
	frame := make([]byte, 4, 4+len(body))
	binary.BigEndian.PutUint32(frame, uint32(len(body)))
	return append(frame, body...), nil
}

// EncodeHelloFrame serializes a payload-less handshake message. Devices do
// not append a CRC to hello messages, so withCRC is false by default; set it
// only when retrying against firmware that expects one.
func EncodeHelloFrame(m *Message, localKey string, connectNonce uint32, withCRC bool) ([]byte, error) {
	body, err := encodeBody(m, codecKeys{localKey: localKey, connectNonce: connectNonce})
	if err != nil {
		return nil, err
	}
	if withCRC {
		body = binary.BigEndian.AppendUint32(body, crc32.ChecksumIEEE(body))
	}
	frame := make([]byte, 4, 4+len(body))
	binary.BigEndian.PutUint32(frame, uint32(len(body)))
	return append(frame, body...), nil
}

// EncodeMQTTFrame serializes a message as an MQTT payload frame. It is the
// local frame without the 4-byte length prefix.
func EncodeMQTTFrame(m *Message, localKey string) ([]byte, error) {
	return encodeBody(m, codecKeys{localKey: localKey})
}

func encodeBody(m *Message, keys codecKeys) ([]byte, error) {
	if len(m.Version) != 3 {
		return nil, fmt.Errorf("protocol version must be 3 bytes, got %q", m.Version)
	}
	body := make([]byte, 0, 23+len(m.Payload))
	body = append(body, m.Version...)
	body = binary.BigEndian.AppendUint32(body, m.Seq)
	body = binary.BigEndian.AppendUint32(body, m.Random)
	body = binary.BigEndian.AppendUint32(body, uint32(m.Timestamp))
	body = binary.BigEndian.AppendUint16(body, m.Protocol)
	if m.Payload != nil {
		encrypted, err := encryptPayload(m, keys)
		if err != nil {
			return nil, fmt.Errorf("encrypt payload: %w", err)
		}
		body = binary.BigEndian.AppendUint16(body, uint16(len(encrypted)))
		body = append(body, encrypted...)
	}
	return body, nil
}

// Decoder decodes framed messages from a byte stream. It tolerates leading
// garbage by scanning for a valid version header, matching the reference
// implementation. It is not safe for concurrent use.
type Decoder struct {
	keys codecKeys
	buf  []byte
}

// NewLocalDecoder returns a decoder for length-prefixed local TCP frames.
func NewLocalDecoder(localKey string, connectNonce, ackNonce uint32) *Decoder {
	return &Decoder{keys: codecKeys{localKey, connectNonce, ackNonce}}
}

// NewMQTTDecoder returns a decoder for MQTT payload frames.
func NewMQTTDecoder(localKey string) *Decoder {
	return &Decoder{keys: codecKeys{localKey: localKey}}
}

// Feed adds bytes to the decoder buffer.
func (d *Decoder) Feed(data []byte) {
	d.buf = append(d.buf, data...)
}

// Next returns the next complete message. It returns io.EOF when no complete
// message remains buffered. CRC mismatches are reported as errors.
func (d *Decoder) Next() (*Message, error) {
	for {
		start, version := scanVersion(d.buf)
		if start < 0 {
			// Keep a small tail in case a version header straddles chunks.
			if len(d.buf) > 3 {
				d.buf = append([]byte(nil), d.buf[len(d.buf)-3:]...)
			}
			return nil, io.EOF
		}
		if start > 0 {
			d.buf = d.buf[start:]
		}
		msg, consumed, err := parseFrame(d.buf, version, d.keys)
		if err != nil {
			return nil, err
		}
		if msg == nil {
			return nil, io.EOF
		}
		d.buf = d.buf[consumed:]
		return msg, nil
	}
}

// scanVersion finds the offset of the first valid version header.
func scanVersion(data []byte) (int, string) {
	for i := 0; i+3 <= len(data); i++ {
		for _, v := range validVersions {
			if bytes.Equal(data[i:i+3], v) {
				return i, string(v)
			}
		}
	}
	return -1, ""
}

// parseFrame parses one message from data. It returns (nil, 0, nil) when the
// buffer holds an incomplete frame, and the consumed byte count otherwise.
// A payload-less frame (hello) has no length field: it ends after the
// 17-byte header, and any trailing bytes are swept by the next version scan.
func parseFrame(data []byte, version string, keys codecKeys) (*Message, int, error) {
	if len(data) < 17 {
		return nil, 0, nil
	}
	m := &Message{
		Version:   version,
		Seq:       binary.BigEndian.Uint32(data[3:7]),
		Random:    binary.BigEndian.Uint32(data[7:11]),
		Timestamp: int32(binary.BigEndian.Uint32(data[11:15])),
		Protocol:  binary.BigEndian.Uint16(data[15:17]),
	}
	if len(data) < 19 {
		m.Payload = nil
		return m, 17, nil
	}
	payloadLen := int(binary.BigEndian.Uint16(data[17:19]))
	if payloadLen == 0 {
		// Zero-length payload: the reference parser consumes four bytes here.
		m.Payload = nil
		if len(data) >= 21 {
			return m, 21, nil
		}
		return m, 19, nil
	}
	end := 19 + payloadLen
	if len(data) < end {
		return nil, 0, nil
	}
	plain, err := decryptPayload(data[19:end], keys, m)
	if err != nil {
		return nil, 0, fmt.Errorf("decrypt %s payload: %w", version, err)
	}
	m.Payload = plain
	if len(data) < end+4 {
		return m, end, nil
	}
	if got := binary.BigEndian.Uint32(data[end : end+4]); got != crc32.ChecksumIEEE(data[:end]) {
		return nil, 0, errors.New("crc32 mismatch")
	}
	return m, end + 4, nil
}

// RandomInt returns a random int in [min, max].
func RandomInt(min, max int) int {
	return min + mrand.Intn(max-min+1)
}

// RandomHex returns n random bytes hex encoded.
func RandomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// MD5Hex returns the md5 hex digest of s.
func MD5Hex(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}
