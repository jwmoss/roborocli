package roborock

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"testing"
	"time"
)

// fakeVacuum is a minimal device simulator: it completes the HELLO handshake
// and answers RPC calls from a method table.
type fakeVacuum struct {
	t        *testing.T
	port     int
	key      string
	ackNonce uint32
	replies  map[string]any
}

func newFakeVacuum(t *testing.T, key string, replies map[string]any) *fakeVacuum {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	v := &fakeVacuum{t: t, port: ln.Addr().(*net.TCPAddr).Port, key: key, replies: replies}
	go v.serve(ln)
	t.Cleanup(func() { ln.Close() })
	return v
}

func (v *fakeVacuum) serve(ln net.Listener) {
	conn, err := ln.Accept()
	if err != nil {
		return
	}
	defer conn.Close()
	decoder := NewLocalDecoder(v.key, 0, 0)
	buf := make([]byte, 8192)
	for {
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		n, err := conn.Read(buf)
		if n > 0 {
			decoder.Feed(buf[:n])
			for {
				msg, derr := decoder.Next()
				if derr == io.EOF {
					break // need more bytes
				}
				if derr != nil {
					v.t.Errorf("vacuum decode: %v", derr)
					return
				}
				if msg == nil {
					break
				}
				v.handle(conn, msg)
			}
		}
		if err != nil {
			return
		}
	}
}

func (v *fakeVacuum) handle(conn net.Conn, msg *Message) {
	switch msg.Protocol {
	case ProtoHelloRequest:
		// Reply with a hello response using protocol 1.0. The ack nonce
		// matters only for L01, so zero is fine here.
		reply := &Message{
			Version:   VersionV1,
			Seq:       msg.Seq,
			Random:    4242,
			Timestamp: nowTimestamp(),
			Protocol:  ProtoHelloResponse,
		}
		frame, err := EncodeHelloFrame(reply, v.key, 0, false)
		if err != nil {
			v.t.Errorf("encode hello reply: %v", err)
			return
		}
		conn.Write(frame)
	case ProtoGeneralRequest:
		if msg.Payload == nil {
			return
		}
		var outer struct {
			DPS map[string]json.RawMessage `json:"dps"`
		}
		if err := json.Unmarshal(msg.Payload, &outer); err != nil {
			v.t.Errorf("vacuum payload: %v", err)
			return
		}
		raw, ok := outer.DPS["101"]
		if !ok {
			return
		}
		innerBytes, err := dpsValue(raw)
		if err != nil {
			v.t.Errorf("vacuum dps unwrap: %v", err)
			return
		}
		var inner struct {
			ID     uint32 `json:"id"`
			Method string `json:"method"`
		}
		if err := json.Unmarshal(innerBytes, &inner); err != nil {
			v.t.Errorf("vacuum inner: %v", err)
			return
		}
		result, found := v.replies[inner.Method]
		if !found {
			// Mirror a real device's reply to an unknown method.
			result = "unknown_method"
		}
		resultBytes, _ := json.Marshal(result)
		replyInner, _ := json.Marshal(map[string]any{"id": inner.ID, "result": json.RawMessage(resultBytes)})
		outPayload, _ := json.Marshal(map[string]any{
			"dps": map[string]string{"102": string(replyInner)},
			"t":   nowTimestamp(),
		})
		reply := &Message{
			Version:   VersionV1,
			Seq:       uint32(RandomInt(100000, 999999)),
			Random:    uint32(RandomInt(10000, 99999)),
			Timestamp: nowTimestamp(),
			Protocol:  ProtoGeneralResponse,
			Payload:   outPayload,
		}
		frame, err := EncodeLocalFrame(reply, v.key, 0, 0)
		if err != nil {
			v.t.Errorf("encode reply: %v", err)
			return
		}
		conn.Write(frame)
	case ProtoPingRequest:
		reply := &Message{
			Version:   VersionV1,
			Seq:       msg.Seq,
			Random:    msg.Random,
			Timestamp: nowTimestamp(),
			Protocol:  ProtoPingResponse,
		}
		frame, err := EncodeHelloFrame(reply, v.key, 0, false)
		if err == nil {
			conn.Write(frame)
		}
	}
}

func TestLocalClientConnectAndCall(t *testing.T) {
	key := "local_key_16chr"
	vac := newFakeVacuum(t, key, map[string]any{
		CmdGetStatus: map[string]any{"battery": 100, "state": 8},
		"get_ok":     []any{"ok"},
	})

	client := &LocalClient{Host: "127.0.0.1", Port: vac.port, LocalKey: key, DUID: "dev1", Timeout: 3 * time.Second}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer client.Close()

	if client.version != VersionV1 {
		t.Fatalf("negotiated version = %q, want %q", client.version, VersionV1)
	}

	res, err := client.Call(ctx, CmdGetStatus, nil)
	if err != nil {
		t.Fatalf("Call get_status: %v", err)
	}
	var status struct {
		Battery int `json:"battery"`
		State   int `json:"state"`
	}
	if err := json.Unmarshal(res, &status); err != nil {
		t.Fatalf("decode result %s: %v", res, err)
	}
	if status.Battery != 100 || status.State != 8 {
		t.Fatalf("unexpected result: %+v", status)
	}
}

func TestLocalClientCommandWithParams(t *testing.T) {
	key := "local_key_16chr"
	vac := newFakeVacuum(t, key, map[string]any{CmdAppSegmentClean: []any{"ok"}})

	client := &LocalClient{Host: "127.0.0.1", Port: vac.port, LocalKey: key, Timeout: 3 * time.Second}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer client.Close()

	res, err := client.Call(ctx, CmdAppSegmentClean, []int{16, 17})
	if err != nil {
		t.Fatalf("Call app_segment_clean: %v", err)
	}
	if string(res) != `["ok"]` {
		t.Fatalf("result = %s, want [\"ok\"]", res)
	}
}

func TestLocalClientUnknownMethod(t *testing.T) {
	key := "local_key_16chr"
	vac := newFakeVacuum(t, key, map[string]any{})

	client := &LocalClient{Host: "127.0.0.1", Port: vac.port, LocalKey: key, Timeout: 3 * time.Second}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer client.Close()

	res, err := client.Call(ctx, "nonexistent_method", nil)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if string(res) != `"unknown_method"` {
		t.Fatalf("result = %s, want \"unknown_method\"", res)
	}
}

func TestLocalClientConnectRefused(t *testing.T) {
	client := &LocalClient{Host: "127.0.0.1", Port: 1, LocalKey: "local_key_16chr", Timeout: time.Second}
	if err := client.Connect(context.Background()); err == nil {
		t.Fatal("expected connection error")
	}
}

func TestBrokerURL(t *testing.T) {
	cases := []struct {
		in     string
		host   string
		port   int
		useTLS bool
	}{
		{"ssl://mqtt-roborock.com:8883", "mqtt-roborock.com", 8883, true},
		{"mqtt-roborock.com:8883", "mqtt-roborock.com", 8883, false},
		{"ssl://mqtt.roborock.com", "mqtt.roborock.com", 8883, true},
	}
	for _, c := range cases {
		host, port, tlsFlag, err := BrokerURL(c.in)
		if err != nil {
			t.Fatalf("BrokerURL(%q): %v", c.in, err)
		}
		if host != c.host || port != c.port || tlsFlag != c.useTLS {
			t.Fatalf("BrokerURL(%q) = %s:%d tls=%v", c.in, host, port, tlsFlag)
		}
	}
}

func TestRRiotDerivations(t *testing.T) {
	rriot := RRiot{U: "user1", S: "sess1", H: "hash1", K: "key1"}
	// Reference values computed from python-roborock's create_mqtt_params.
	wantUser := MD5Hex("user1:key1")[2:10]
	if got := rriot.HashedUser(); got != wantUser {
		t.Fatalf("HashedUser = %q, want %q", got, wantUser)
	}
	wantPass := MD5Hex("sess1:key1")[16:]
	if got := rriot.BrokerPassword(); got != wantPass {
		t.Fatalf("BrokerPassword = %q, want %q", got, wantPass)
	}
	if rriot.SecurityEndpoint() == "" {
		t.Fatal("SecurityEndpoint is empty")
	}
}

func TestTopics(t *testing.T) {
	rriot := RRiot{U: "abc", S: "s", H: "h", K: "kk"}
	m := NewMQTTClient(rriot)
	hash := rriot.HashedUser()
	if got := m.publishTopic("duid1"); got != "rr/m/i/abc/"+hash+"/duid1" {
		t.Fatalf("publishTopic = %s", got)
	}
	if got := m.subscribeTopic("duid1"); got != "rr/m/o/abc/"+hash+"/duid1" {
		t.Fatalf("subscribeTopic = %s", got)
	}
}
