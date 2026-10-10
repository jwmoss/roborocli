package roborock

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
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
	}
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
	opts := mqtt.NewClientOptions().
		AddBroker(fmt.Sprintf("%s:%d", host, port)).
		SetClientID(fmt.Sprintf("roborocli-%d", time.Now().UnixNano()%100000)).
		SetUsername(m.RRiot.HashedUser()).
		SetPassword(m.RRiot.BrokerPassword()).
		SetConnectTimeout(m.Timeout).
		SetAutoReconnect(true).
		SetCleanSession(true)
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
	sub := client.Subscribe(topic, 1, func(_ mqtt.Client, msg mqtt.Message) {
		m.onMessage(msg.Payload())
	})
	if !sub.WaitTimeout(m.Timeout) {
		return fmt.Errorf("mqtt subscribe to %s timed out", topic)
	}
	if err := sub.Error(); err != nil {
		return fmt.Errorf("mqtt subscribe: %w", err)
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
	msg, err := decoder.Next()
	if err != nil || msg == nil || msg.Payload == nil {
		return
	}
	var outer struct {
		DPS map[string]json.RawMessage `json:"dps"`
	}
	if err := json.Unmarshal(msg.Payload, &outer); err != nil {
		return
	}
	raw, ok := outer.DPS[fmt.Sprintf("%d", ProtoRPCResponse)]
	if !ok {
		return
	}
	var inner struct {
		ID     uint32          `json:"id"`
		Result json.RawMessage `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(raw, &inner); err != nil {
		return
	}
	m.mu.Lock()
	ch, ok := m.pending[inner.ID]
	if ok {
		delete(m.pending, inner.ID)
	}
	m.mu.Unlock()
	if !ok {
		return
	}
	if len(inner.Error) > 0 && string(inner.Error) != "null" {
		ch <- rpcResult{err: fmt.Errorf("device error: %s", string(inner.Error))}
		close(ch)
		return
	}
	ch <- rpcResult{result: inner.Result}
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
	if m.client == nil {
		return nil, fmt.Errorf("mqtt not connected")
	}
	if params == nil {
		params = []any{}
	}
	m.mu.Lock()
	m.nextID = uint32(RandomInt(10000, 32767))
	id := m.nextID
	ch := make(chan rpcResult, 1)
	m.pending[id] = ch
	localKey := m.localKey
	m.mu.Unlock()

	security := map[string]string{
		"endpoint": m.RRiot.SecurityEndpoint(),
		"nonce":    RandomHex(16),
	}
	payload, err := encodeRPCPayload(id, method, params, security)
	if err != nil {
		m.clearPending(id)
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
		m.clearPending(id)
		return nil, err
	}
	token := m.client.Publish(m.publishTopic(duid), 0, false, frame)
	if !token.WaitTimeout(m.Timeout) {
		m.clearPending(id)
		return nil, fmt.Errorf("mqtt publish timed out")
	}
	if err := token.Error(); err != nil {
		m.clearPending(id)
		return nil, fmt.Errorf("mqtt publish: %w", err)
	}

	deadline := time.NewTimer(m.Timeout)
	defer deadline.Stop()
	select {
	case res := <-ch:
		if res.err != nil {
			return nil, res.err
		}
		return res.result, nil
	case <-deadline.C:
		m.clearPending(id)
		return nil, fmt.Errorf("timeout waiting for %s", method)
	}
}

func (m *MQTTClient) clearPending(id uint32) {
	m.mu.Lock()
	delete(m.pending, id)
	m.mu.Unlock()
}

// Close disconnects from the broker.
func (m *MQTTClient) Close() {
	if m.client != nil && m.client.IsConnected() {
		m.client.Disconnect(250)
	}
}
