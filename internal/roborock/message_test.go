package roborock

import (
	"bytes"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"testing"
)

// Golden values generated independently from the reference algorithm
// (python-roborock roborock/protocol.py Utils.encode_timestamp and the
// V1/L01 key schedules).
const (
	goldenTS        = int32(1770000000)
	goldenLocalKey  = "local_key_16chr"
	goldenEncodeTs  = "e8009860"
	goldenV1KeyHex  = "8633efc5ca04d5c87aecdd47828bb05b"
	goldenL01KeyHex = "b0147206d9b3c78b263dc8d658bb5e3f44431780045e34df78c27e2f0d1c3929"
)

func TestEncodeTimestamp(t *testing.T) {
	if got := encodeTimestamp(goldenTS); got != goldenEncodeTs {
		t.Fatalf("encodeTimestamp(%d) = %q, want %q", goldenTS, got, goldenEncodeTs)
	}
}

func TestV1KeyGolden(t *testing.T) {
	sum := md5.Sum([]byte(goldenEncodeTs + goldenLocalKey + Salt))
	if got := hex.EncodeToString(sum[:]); got != goldenV1KeyHex {
		t.Fatalf("v1 key = %s, want %s", got, goldenV1KeyHex)
	}
	if got := hex.EncodeToString(v1Key(goldenTS, goldenLocalKey)); got != goldenV1KeyHex {
		t.Fatalf("v1Key = %s, want %s", got, goldenV1KeyHex)
	}
}

func TestL01KeyGolden(t *testing.T) {
	sum := sha256.Sum256([]byte(goldenEncodeTs + goldenLocalKey + Salt))
	if got := hex.EncodeToString(sum[:]); got != goldenL01KeyHex {
		t.Fatalf("l01 key = %s, want %s", got, goldenL01KeyHex)
	}
	if got := hex.EncodeToString(l01Key(goldenTS, goldenLocalKey)); got != goldenL01KeyHex {
		t.Fatalf("l01Key = %s, want %s", got, goldenL01KeyHex)
	}
}

func TestV1FrameRoundTrip(t *testing.T) {
	msg := &Message{
		Version:   VersionV1,
		Seq:       1,
		Random:    1234,
		Timestamp: goldenTS,
		Protocol:  ProtoRPCRequest,
		Payload:   []byte(`{"dps":{"101":"{\"id\":1,\"method\":\"get_status\"}"}}`),
	}
	frame, err := EncodeLocalFrame(msg, goldenLocalKey, 0, 0)
	if err != nil {
		t.Fatalf("EncodeLocalFrame: %v", err)
	}
	// Frame layout: 4-byte length, 17-byte header, 2-byte payload length,
	// padded payload, 4-byte CRC.
	if len(frame) < 4+17+2+4 {
		t.Fatalf("frame too short: %d", len(frame))
	}
	body := frame[4:]
	if want := uint32(len(body)); beUint32(frame[:4]) != want {
		t.Fatalf("length prefix = %d, want %d", beUint32(frame[:4]), want)
	}

	dec := NewLocalDecoder(goldenLocalKey, 0, 0)
	dec.Feed(frame)
	got, err := dec.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if got.Protocol != msg.Protocol || got.Seq != msg.Seq || got.Random != msg.Random ||
		got.Timestamp != msg.Timestamp || !bytes.Equal(got.Payload, msg.Payload) {
		t.Fatalf("round trip mismatch: %+v", got)
	}
	if _, err := dec.Next(); err != io.EOF {
		t.Fatalf("expected EOF after one message, got %v", err)
	}
}

func TestL01FrameRoundTrip(t *testing.T) {
	msg := &Message{
		Version:   VersionL01,
		Seq:       7,
		Random:    99,
		Timestamp: goldenTS,
		Protocol:  ProtoGeneralRequest,
		Payload:   []byte(`hello world payload`),
	}
	frame, err := EncodeLocalFrame(msg, goldenLocalKey, 0x0A0B0C0D, 0x11223344)
	if err != nil {
		t.Fatalf("EncodeLocalFrame: %v", err)
	}
	dec := NewLocalDecoder(goldenLocalKey, 0x0A0B0C0D, 0x11223344)
	dec.Feed(frame)
	got, err := dec.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if !bytes.Equal(got.Payload, msg.Payload) {
		t.Fatalf("payload = %q, want %q", got.Payload, msg.Payload)
	}
}

func TestDecoderToleratesGarbageAndPrefixes(t *testing.T) {
	msg := &Message{
		Version:   VersionV1,
		Seq:       2,
		Random:    5,
		Timestamp: goldenTS,
		Protocol:  ProtoGeneralRequest,
		Payload:   []byte("test_payload"),
	}
	frame, err := EncodeLocalFrame(msg, goldenLocalKey, 0, 0)
	if err != nil {
		t.Fatalf("EncodeLocalFrame: %v", err)
	}
	// The reference decoder tolerates garbage before a frame, including the
	// 4-byte length prefix of a following frame.
	garbage := []byte{0x00, 0x00, 0x05, 0xa1, 0xff, 0xff}
	dec := NewLocalDecoder(goldenLocalKey, 0, 0)
	dec.Feed(garbage)
	if _, err := dec.Next(); err != io.EOF {
		t.Fatalf("expected EOF for garbage only, got %v", err)
	}
	dec.Feed(frame)
	got, err := dec.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if !bytes.Equal(got.Payload, msg.Payload) {
		t.Fatalf("payload = %q, want %q", got.Payload, msg.Payload)
	}
}

func TestMQTTFrameRoundTrip(t *testing.T) {
	msg := &Message{
		Version:   VersionV1,
		Seq:       42,
		Random:    777,
		Timestamp: goldenTS,
		Protocol:  ProtoRPCRequest,
		Payload:   []byte(`{"dps":{"101":"x"},"t":1770000000}`),
	}
	frame, err := EncodeMQTTFrame(msg, goldenLocalKey)
	if err != nil {
		t.Fatalf("EncodeMQTTFrame: %v", err)
	}
	if bytes.Equal(frame[:3], []byte("1.0")) == false {
		t.Fatalf("frame should start with version, got %q", frame[:3])
	}
	dec := NewMQTTDecoder(goldenLocalKey)
	dec.Feed(frame)
	got, err := dec.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if !bytes.Equal(got.Payload, msg.Payload) {
		t.Fatalf("payload = %q, want %q", got.Payload, msg.Payload)
	}
}

func TestSplitFrameAcrossFeeds(t *testing.T) {
	msg := &Message{
		Version:   VersionV1,
		Seq:       3,
		Random:    8,
		Timestamp: goldenTS,
		Protocol:  ProtoGeneralRequest,
		Payload:   []byte("split across chunks"),
	}
	frame, err := EncodeLocalFrame(msg, goldenLocalKey, 0, 0)
	if err != nil {
		t.Fatalf("EncodeLocalFrame: %v", err)
	}
	dec := NewLocalDecoder(goldenLocalKey, 0, 0)
	dec.Feed(frame[:5])
	if _, err := dec.Next(); err != io.EOF {
		t.Fatalf("expected EOF for partial frame, got %v", err)
	}
	dec.Feed(frame[5:])
	got, err := dec.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if !bytes.Equal(got.Payload, msg.Payload) {
		t.Fatalf("payload = %q, want %q", got.Payload, msg.Payload)
	}
}

func TestHelloFrameWithoutPayload(t *testing.T) {
	msg := &Message{
		Version:   VersionV1,
		Seq:       1,
		Random:    10000,
		Timestamp: goldenTS,
		Protocol:  ProtoHelloRequest,
	}
	frame, err := EncodeHelloFrame(msg, goldenLocalKey, 10000, false)
	if err != nil {
		t.Fatalf("EncodeHelloFrame: %v", err)
	}
	wantLen := 4 + 3 + 4 + 4 + 4 + 2 // prefix + header, no length field, no crc
	if len(frame) != wantLen {
		t.Fatalf("hello frame length = %d, want %d", len(frame), wantLen)
	}
	dec := NewLocalDecoder(goldenLocalKey, 0, 0)
	dec.Feed(frame)
	got, err := dec.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if got.Protocol != ProtoHelloRequest || got.Payload != nil {
		t.Fatalf("hello decode mismatch: %+v", got)
	}
}

func TestCRCMismatchRejected(t *testing.T) {
	msg := &Message{
		Version:   VersionV1,
		Seq:       4,
		Random:    9,
		Timestamp: goldenTS,
		Protocol:  ProtoGeneralRequest,
		Payload:   []byte("corrupt me"),
	}
	frame, err := EncodeLocalFrame(msg, goldenLocalKey, 0, 0)
	if err != nil {
		t.Fatalf("EncodeLocalFrame: %v", err)
	}
	frame[len(frame)-1] ^= 0xFF
	dec := NewLocalDecoder(goldenLocalKey, 0, 0)
	dec.Feed(frame)
	if _, err := dec.Next(); err == nil {
		t.Fatal("expected CRC mismatch error")
	}
}

func beUint32(b []byte) uint32 {
	var v uint32
	for _, x := range b {
		v = v<<8 | uint32(x)
	}
	return v
}
