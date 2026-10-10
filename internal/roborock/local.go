package roborock

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
	"time"
)

// LocalPort is the vacuum's TCP port for direct local control. Confirmed in
// the app's LocalClientService (tcp://host:58867).
const LocalPort = 58867

// LocalClient is a direct TCP connection to a vacuum. It performs the
// HELLO handshake, tracks the negotiated protocol version, keeps a ping
// alive, and correlates RPC requests with responses.
type LocalClient struct {
	Host     string
	LocalKey string
	DUID     string
	Port     int // device port; defaults to LocalPort
	Timeout  time.Duration

	mu        sync.Mutex
	conn      net.Conn
	version   string
	connNonce uint32
	ackNonce  uint32
	nextID    uint32
	pending   map[uint32]chan rpcResult
	readErr   error
	done      chan struct{}
}

type rpcResult struct {
	result json.RawMessage
	err    error
}

// Connect dials the vacuum and negotiates the protocol version. It tries V1
// first, then L01, matching the app's preference for 1.0 devices.
func (c *LocalClient) Connect(ctx context.Context) error {
	c.Timeout = orDuration(c.Timeout, 10*time.Second)
	c.connNonce = uint32(RandomInt(10000, 32767))
	c.nextID = uint32(RandomInt(10000, 32767))
	c.pending = map[uint32]chan rpcResult{}
	c.done = make(chan struct{})

	conn, err := net.DialTimeout("tcp", net.JoinHostPort(c.Host, itoa(orInt(c.Port, LocalPort))), c.Timeout)
	if err != nil {
		return fmt.Errorf("connect to %s: %w", c.Host, err)
	}
	c.conn = conn

	// Negotiate protocol. Try "1.0" then "L01"; a failed hello shows up as a
	// timeout because the device stays silent on an unknown version.
	versions := []string{VersionV1, VersionL01}
	if c.version != "" {
		versions = []string{c.version}
	}
	var lastErr error
	connected := false
	for _, version := range versions {
		if err := c.hello(ctx, version); err != nil {
			lastErr = err
			continue
		}
		c.version = version
		connected = true
		break
	}
	if !connected {
		conn.Close()
		c.conn = nil
		return fmt.Errorf("hello: %w", lastErr)
	}

	go c.readLoop()
	return nil
}

// hello sends a HELLO_REQUEST for a version and waits for the response.
func (c *LocalClient) hello(ctx context.Context, version string) error {
	msg := &Message{
		Version:   version,
		Seq:       1,
		Random:    c.connNonce,
		Timestamp: nowTimestamp(),
		Protocol:  ProtoHelloRequest,
	}
	frame, err := EncodeHelloFrame(msg, c.LocalKey, c.connNonce, false)
	if err != nil {
		return err
	}
	deadline := time.Now().Add(c.Timeout)
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}
	_ = c.conn.SetDeadline(deadline)
	if _, err := c.conn.Write(frame); err != nil {
		return fmt.Errorf("write hello: %w", err)
	}

	decoder := NewLocalDecoder(c.LocalKey, c.connNonce, 0)
	buf := make([]byte, 4096)
	for {
		n, err := c.conn.Read(buf)
		if err != nil {
			return fmt.Errorf("read hello response: %w", err)
		}
		decoder.Feed(buf[:n])
		msg, err := decoder.Next()
		if err != nil {
			return fmt.Errorf("decode hello response: %w", err)
		}
		if msg == nil {
			continue
		}
		if msg.Protocol != ProtoHelloResponse {
			continue
		}
		if version == VersionL01 {
			// L01 derives a key from the acknowledged nonce; subsequent
			// messages include the ack in the AAD.
			c.mu.Lock()
			c.ackNonce = msg.Random
			c.mu.Unlock()
		}
		return nil
	}
}

// readLoop decodes incoming frames and routes RPC responses.
func (c *LocalClient) readLoop() {
	defer close(c.done)
	decoder := NewLocalDecoder(c.LocalKey, c.connNonce, c.ackNonce)
	buf := make([]byte, 8192)
	for {
		n, err := c.conn.Read(buf)
		if n > 0 {
			decoder.Feed(buf[:n])
			for {
				msg, derr := decoder.Next()
				if derr == io.EOF {
					break // need more bytes
				}
				if derr != nil {
					c.setReadErr(derr)
					return
				}
				if msg == nil {
					break
				}
				c.dispatch(msg)
			}
		}
		if err != nil {
			c.setReadErr(err)
			return
		}
	}
}

func (c *LocalClient) setReadErr(err error) {
	c.mu.Lock()
	if c.readErr == nil {
		c.readErr = err
	}
	c.mu.Unlock()
}

func (c *LocalClient) dispatch(msg *Message) {
	if msg.Payload == nil {
		return
	}
	// RPC responses arrive as a dps map keyed by 102.
	var outer struct {
		DPS map[string]json.RawMessage `json:"dps"`
	}
	if err := json.Unmarshal(msg.Payload, &outer); err != nil {
		return
	}
	for key, raw := range outer.DPS {
		if key != fmt.Sprintf("%d", ProtoRPCResponse) {
			continue
		}
		innerBytes, err := dpsValue(raw)
		if err != nil {
			continue
		}
		var inner struct {
			ID     uint32          `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  json.RawMessage `json:"error"`
		}
		if err := json.Unmarshal(innerBytes, &inner); err != nil {
			continue
		}
		c.mu.Lock()
		ch, ok := c.pending[inner.ID]
		if ok {
			delete(c.pending, inner.ID)
		}
		c.mu.Unlock()
		if !ok {
			continue
		}
		if len(inner.Error) > 0 && string(inner.Error) != "null" {
			ch <- rpcResult{err: fmt.Errorf("device error: %s", string(inner.Error))}
			close(ch)
			continue
		}
		ch <- rpcResult{result: inner.Result}
		close(ch)
	}
}

// Call sends an RPC command and waits for the matching response. params may
// be nil (sent as []), a slice, or a single value.
func (c *LocalClient) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if c.conn == nil {
		return nil, fmt.Errorf("not connected")
	}
	if params == nil {
		params = []any{}
	}

	c.mu.Lock()
	c.nextID = uint32(RandomInt(10000, 32767))
	id := c.nextID
	ch := make(chan rpcResult, 1)
	c.pending[id] = ch
	version := c.version
	connNonce, ackNonce := c.connNonce, c.ackNonce
	c.mu.Unlock()

	payload, err := encodeRPCPayload(id, method, params, nil)
	if err != nil {
		c.clearPending(id)
		return nil, err
	}

	msg := &Message{
		Version:   version,
		Seq:       uint32(RandomInt(100000, 999999)),
		Random:    uint32(RandomInt(10000, 99999)),
		Timestamp: nowTimestamp(),
		Protocol:  ProtoGeneralRequest,
		Payload:   payload,
	}
	frame, err := EncodeLocalFrame(msg, c.LocalKey, connNonce, ackNonce)
	if err != nil {
		return nil, err
	}

	deadline := time.Now().Add(c.Timeout)
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()

	_ = c.conn.SetWriteDeadline(time.Now().Add(c.Timeout))
	if _, err := c.conn.Write(frame); err != nil {
		c.clearPending(id)
		return nil, fmt.Errorf("write request: %w", err)
	}

	select {
	case res := <-ch:
		if res.err != nil {
			return nil, res.err
		}
		return res.result, nil
	case <-timer.C:
		c.clearPending(id)
		return nil, fmt.Errorf("timeout waiting for %s", method)
	case <-c.done:
		c.mu.Lock()
		err := c.readErr
		c.mu.Unlock()
		return nil, fmt.Errorf("connection closed: %v", err)
	}
}

func (c *LocalClient) clearPending(id uint32) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

// Ping sends a protocol-level PING_REQUEST and waits for the response.
func (c *LocalClient) Ping(ctx context.Context) error {
	if c.conn == nil {
		return fmt.Errorf("not connected")
	}
	c.mu.Lock()
	version := c.version
	connNonce, ackNonce := c.connNonce, c.ackNonce
	c.mu.Unlock()

	msg := &Message{
		Version:   version,
		Seq:       uint32(RandomInt(100000, 999999)),
		Random:    uint32(RandomInt(10000, 99999)),
		Timestamp: nowTimestamp(),
		Protocol:  ProtoPingRequest,
	}
	frame, err := EncodeLocalFrame(msg, c.LocalKey, connNonce, ackNonce)
	if err != nil {
		return err
	}
	_ = c.conn.SetWriteDeadline(time.Now().Add(c.Timeout))
	_, err = c.conn.Write(frame)
	return err
}

// Close closes the underlying connection.
func (c *LocalClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == nil {
		return nil
	}
	err := c.conn.Close()
	c.conn = nil
	return err
}

// encodeRPCPayload builds the {"dps":{"101":"..."},"t":ts} envelope that
// carries one RPC request. security is the optional cloud security block.
func encodeRPCPayload(id uint32, method string, params any, security map[string]string) ([]byte, error) {
	inner := map[string]any{"id": id, "method": method, "params": params}
	if security != nil {
		inner["security"] = security
	}
	innerBytes, err := json.Marshal(inner)
	if err != nil {
		return nil, err
	}
	outer := map[string]any{
		"dps": map[string]string{
			fmt.Sprintf("%d", ProtoRPCRequest): string(innerBytes),
		},
		"t": nowTimestamp(),
	}
	return json.Marshal(outer)
}

// dpsValue unwraps one data point. The dps map carries each point as a JSON
// string containing the inner payload, so callers unmarshal twice.
func dpsValue(raw json.RawMessage) (json.RawMessage, error) {
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return nil, err
	}
	return json.RawMessage(text), nil
}

// nowTimestamp returns the current unix timestamp in seconds.
func nowTimestamp() int32 { return int32(time.Now().Unix()) }

// orDuration returns d if set, otherwise fallback.
func orDuration(d, fallback time.Duration) time.Duration {
	if d > 0 {
		return d
	}
	return fallback
}

// orInt returns v if positive, otherwise fallback.
func orInt(v, fallback int) int {
	if v > 0 {
		return v
	}
	return fallback
}

// itoa converts an int to a string.
func itoa(v int) string { return strconv.Itoa(v) }
