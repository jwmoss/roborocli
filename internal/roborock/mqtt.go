package roborock

import (
	"context"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

// MQTTClient talks to a vacuum through the Roborock cloud broker. Topics:
//
//	rr/m/i/<u>/<hash>/<duid>  app to device
//	rr/m/o/<u>/<hash>/<duid>  device to app
//
// where <hash> is md5(u:k)[2:10]. The broker credentials are md5(u:k)[2:10]
// as the username and md5(s:k)[16:] as the password.
type MQTTClient struct {
	RRiot   RRiot
	Timeout time.Duration

	client     mqtt.Client
	mu         sync.Mutex
	pending    map[uint32]chan rpcResult
	nextID     uint32
	subscribed map[string]bool
	localKey   string
	stream     eventStream
	blobs      map[uint32][]byte
	family     string
	device     Device
	product    Product
	familyMu   sync.Mutex
	updates    chan map[string]json.RawMessage
	mapUpdates chan []byte
}

// BrokerURL parses the MQTT broker reference into host, port, and TLS flag.
func BrokerURL(mqttURL string) (host string, port int, useTLS bool, err error) {
	raw := strings.TrimSpace(mqttURL)
	scheme := ""
	remainder := raw
	if idx := strings.Index(raw, "://"); idx >= 0 {
		scheme, remainder = raw[:idx], raw[idx+3:]
	}
	// Only an explicit ssl/tls/mqtts scheme implies TLS, matching the app.
	useTLS = scheme == "ssl" || scheme == "tls" || scheme == "mqtts"
	remainder = strings.TrimSuffix(remainder, "/")
	host = remainder
	port = 8883
	if idx := strings.LastIndex(remainder, ":"); idx > 0 {
		host = remainder[:idx]
		fmt.Sscanf(remainder[idx+1:], "%d", &port)
	}
	if host == "" {
		return "", 0, false, fmt.Errorf("invalid mqtt url %q", mqttURL)
	}
	return host, port, useTLS, nil
}

// NewMQTTClient builds a client for one account.
func NewMQTTClient(rriot RRiot) *MQTTClient {
	return &MQTTClient{
		RRiot:      rriot,
		Timeout:    orDuration(0, 10*time.Second),
		pending:    map[uint32]chan rpcResult{},
		subscribed: map[string]bool{},
		stream:     eventStream{events: make(chan Event, 256)},
		blobs:      map[uint32][]byte{},
		updates:    make(chan map[string]json.RawMessage, 128),
		mapUpdates: make(chan []byte, 16),
	}
}

func mqttProtocolVersion() uint {
	if os.Getenv("ROBOROCLI_MQTT_PROTO") == "4" {
		return 4
	}
	return 5
}

// publishTopic returns the app-to-device topic for a device.
func (m *MQTTClient) publishTopic(duid string) string {
	return fmt.Sprintf("rr/m/i/%s/%s/%s", m.RRiot.U, m.RRiot.HashedUser(), duid)
}

// subscribeTopic returns the device-to-app topic for a device.
func (m *MQTTClient) subscribeTopic(duid string) string {
	return fmt.Sprintf("rr/m/o/%s/%s/%s", m.RRiot.U, m.RRiot.HashedUser(), duid)
}

// Connect opens the broker connection and subscribes to a device topic.
func (m *MQTTClient) Connect(ctx context.Context, duid string) error {
	host, port, useTLS, err := BrokerURL(m.RRiot.R.M)
	if err != nil {
		return err
	}
	scheme := "tcp"
	if useTLS {
		scheme = "ssl"
	}
	opts := mqtt.NewClientOptions().
		AddBroker(fmt.Sprintf("%s://%s:%d", scheme, host, port)).
		SetClientID(fmt.Sprintf("roborocli-%d", time.Now().UnixNano()%100000)).
		SetUsername(m.RRiot.HashedUser()).
		SetPassword(m.RRiot.BrokerPassword()).
		SetProtocolVersion(mqttProtocolVersion()).
		SetConnectTimeout(m.Timeout).
		SetKeepAlive(45 * time.Second).
		SetAutoReconnect(true).
		SetCleanSession(true)
	opts.SetConnectionLostHandler(func(_ mqtt.Client, err error) { m.stream.emit(Event{Kind: "disconnected", Error: err.Error()}) })
	if useTLS {
		opts.SetTLSConfig(&tls.Config{MinVersion: tls.VersionTLS12})
	}
	client := mqtt.NewClient(opts)
	token := client.Connect()
	if !token.WaitTimeout(m.Timeout) {
		return fmt.Errorf("mqtt connect to %s:%d timed out", host, port)
	}
	if err := token.Error(); err != nil {
		return fmt.Errorf("mqtt connect: %w", err)
	}
	m.client = client

	topic := m.subscribeTopic(duid)
	if os.Getenv("ROBOROCLI_MQTT_WILDCARD") == "1" {
		topic = strings.TrimSuffix(topic, "/"+duid) + "/#"
	}
	sub := client.Subscribe(topic, 1, func(_ mqtt.Client, msg mqtt.Message) {
		if os.Getenv("ROBOROCLI_DEBUG_MQTT") == "1" {
			fmt.Fprintf(os.Stderr, "mqtt recv %s %d bytes\n", msg.Topic(), len(msg.Payload()))
		}
		m.onMessage(msg.Payload())
	})
	if !sub.WaitTimeout(m.Timeout) {
		return fmt.Errorf("mqtt subscribe to %s timed out", topic)
	}
	if err := sub.Error(); err != nil {
		return fmt.Errorf("mqtt subscribe: %w", err)
	}
	if os.Getenv("ROBOROCLI_DEBUG_MQTT") == "1" {
		for _, extra := range []string{"rr/m/o/#", "rr/d/#", "rr/#"} {
			token := client.Subscribe(extra, 1, func(_ mqtt.Client, msg mqtt.Message) {
				fmt.Fprintf(os.Stderr, "mqtt extra %s %d bytes\n", msg.Topic(), len(msg.Payload()))
			})
			if !token.WaitTimeout(m.Timeout) {
				fmt.Fprintf(os.Stderr, "mqtt subscribe %s timeout\n", extra)
				continue
			}
			if err := token.Error(); err != nil {
				fmt.Fprintf(os.Stderr, "mqtt subscribe %s error: %v\n", extra, err)
			}
		}
	}
	m.subscribed[topic] = true
	return nil
}

// onMessage decodes an inbound MQTT frame and routes RPC responses.
func (m *MQTTClient) onMessage(payload []byte) {
	m.mu.Lock()
	localKey := m.localKey
	m.mu.Unlock()
	decoder := NewMQTTDecoder(localKey)
	decoder.Feed(payload)
	for {
		msg, err := decoder.Next()
		if os.Getenv("ROBOROCLI_DEBUG_MQTT") == "1" {
			fmt.Fprintf(os.Stderr, "mqtt decode err=%v msg=%v\n", err, msg != nil)
		}
		if err != nil || msg == nil || msg.Payload == nil {
			return
		}
		if m.family != "" && m.family != "v1" {
			m.dispatchFamily(msg)
			continue
		}
		if msg.Protocol == ProtoMapResponse {
			m.dispatchBlob(msg)
			continue
		}
		m.dispatchRPC(msg)
	}
}

func (m *MQTTClient) dispatchRPC(msg *Message) {
	var outer struct {
		DPS map[string]json.RawMessage `json:"dps"`
	}
	if err := json.Unmarshal(msg.Payload, &outer); err != nil {
		return
	}
	m.stream.push(outer.DPS, "v1")
	raw, ok := outer.DPS[fmt.Sprintf("%d", ProtoRPCResponse)]
	if !ok {
		return
	}
	raw, err := dpsValue(raw)
	if err != nil {
		return
	}
	var inner rpcResponse
	if err := json.Unmarshal(raw, &inner); err != nil {
		return
	}
	m.mu.Lock()
	ch, ok := m.pending[inner.ID]
	_, blob := m.blobs[inner.ID]
	if blob && inner.response().err == nil {
		m.mu.Unlock()
		return
	}
	if ok {
		delete(m.pending, inner.ID)
	}
	m.mu.Unlock()
	if !ok {
		return
	}
	ch <- inner.response()
	close(ch)
}

// SetDeviceKey records the device local key used to encode and decode frames.
func (m *MQTTClient) SetDeviceKey(key string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.localKey = key
}

// Call sends an RPC over MQTT with the cloud security envelope and waits for
// the response.
func (m *MQTTClient) Call(ctx context.Context, duid string, method string, params any) (json.RawMessage, error) {
	if method == CmdGetRoomMapping && (m.family == "q7" || m.family == "q10") {
		return m.familyRooms(ctx, duid)
	}
	if m.family != "" && m.family != "v1" {
		return m.callFamily(ctx, duid, method, params)
	}
	return m.call(ctx, duid, method, params, false)
}

func (m *MQTTClient) call(ctx context.Context, duid string, method string, params any, blob bool) (json.RawMessage, error) {
	if m.client == nil {
		return nil, fmt.Errorf("mqtt not connected")
	}
	if params == nil {
		params = []any{}
	}
	m.mu.Lock()
	if m.nextID == 0 {
		m.nextID = uint32(RandomInt(10000, 20000))
	}
	m.nextID++
	if m.nextID > 32767 {
		m.nextID = 10000
	}
	id := m.nextID
	ch := make(chan rpcResult, 1)
	m.pending[id] = ch
	localKey := m.localKey
	m.mu.Unlock()

	security := map[string]string{
		"endpoint": m.RRiot.SecurityEndpoint(),
		"nonce":    RandomHex(16),
	}
	if blob {
		nonce, _ := hex.DecodeString(security["nonce"])
		m.mu.Lock()
		m.blobs[id] = nonce
		m.mu.Unlock()
	}
	defer m.clearPending(id)
	payload, err := encodeRPCPayload(id, method, params, security)
	if err != nil {
		return nil, err
	}
	msg := &Message{
		Version:   VersionV1,
		Seq:       uint32(RandomInt(100000, 999999)),
		Random:    uint32(RandomInt(10000, 99999)),
		Timestamp: nowTimestamp(),
		Protocol:  ProtoRPCRequest,
		Payload:   payload,
	}
	frame, err := EncodeMQTTFrame(msg, localKey)
	if err != nil {
		return nil, err
	}
	if os.Getenv("ROBOROCLI_DEBUG_MQTT") == "1" {
		fmt.Fprintf(os.Stderr, "mqtt publish %s %d bytes\n", m.publishTopic(duid), len(frame))
	}
	token := m.client.Publish(m.publishTopic(duid), 1, false, frame)
	if !token.WaitTimeout(m.Timeout) {
		return nil, fmt.Errorf("mqtt publish timed out")
	}
	if err := token.Error(); err != nil {
		return nil, fmt.Errorf("mqtt publish: %w", err)
	}

	deadline := time.NewTimer(m.Timeout)
	defer deadline.Stop()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case res := <-ch:
		if res.err != nil {
			return nil, res.err
		}
		return res.result, nil
	case <-deadline.C:
		return nil, fmt.Errorf("timeout waiting for %s", method)
	}
}

func (m *MQTTClient) clearPending(id uint32) {
	m.mu.Lock()
	delete(m.pending, id)
	delete(m.blobs, id)
	m.mu.Unlock()
}

// Close disconnects from the broker.
func (m *MQTTClient) Close() {
	if m.client != nil && m.client.IsConnected() {
		m.client.Disconnect(250)
	}
}
