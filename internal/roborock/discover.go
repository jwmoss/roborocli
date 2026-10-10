package roborock

import (
	"crypto/aes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"time"
)

// BroadcastToken is the fixed key for local discovery announcements.
// The vacuum sends these to UDP port 58866.
const BroadcastToken = "qWKYcdQWrbm9hPqe"

// DiscoveredDevice is one vacuum found on the local network.
type DiscoveredDevice struct {
	DUID    string `json:"duid"`
	IP      string `json:"ip"`
	Version string `json:"version"`
}

// Discover listens for vacuum announcements and returns the devices that
// answer before the timeout.
func Discover(timeout time.Duration) ([]DiscoveredDevice, error) {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{Port: 58866})
	if err != nil {
		return nil, fmt.Errorf("listen for vacuum announcements: %w", err)
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(timeout))

	seen := map[string]DiscoveredDevice{}
	buf := make([]byte, 2048)
	for {
		n, _, err := conn.ReadFromUDP(buf)
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				break
			}
			return nil, fmt.Errorf("read vacuum announcement: %w", err)
		}
		device, err := decodeBroadcast(buf[:n])
		if err != nil {
			continue
		}
		seen[device.DUID] = device
	}
	out := make([]DiscoveredDevice, 0, len(seen))
	for _, device := range seen {
		out = append(out, device)
	}
	return out, nil
}

func decodeBroadcast(data []byte) (DiscoveredDevice, error) {
	if len(data) < 9 {
		return DiscoveredDevice{}, fmt.Errorf("announcement too short")
	}
	version := string(data[:3])
	var payload []byte
	var err error
	if version == VersionL01 {
		payload, err = decryptL01Broadcast(data)
	} else {
		payload, err = decryptV1Broadcast(data)
	}
	if err != nil {
		return DiscoveredDevice{}, err
	}
	var body struct {
		DUID string `json:"duid"`
		IP   string `json:"ip"`
	}
	if err := json.Unmarshal(payload, &body); err != nil {
		return DiscoveredDevice{}, err
	}
	if body.DUID == "" || body.IP == "" {
		return DiscoveredDevice{}, fmt.Errorf("announcement missing duid or ip")
	}
	return DiscoveredDevice{DUID: body.DUID, IP: body.IP, Version: version}, nil
}

func decryptV1Broadcast(data []byte) ([]byte, error) {
	if len(data) < 11 {
		return nil, fmt.Errorf("broadcast too short")
	}
	payloadLen := int(binary.BigEndian.Uint16(data[9:11]))
	if payloadLen == 0 || 11+payloadLen > len(data) {
		return nil, fmt.Errorf("invalid broadcast payload length")
	}
	return decryptECB(data[11:11+payloadLen], []byte(BroadcastToken))
}

func decryptL01Broadcast(data []byte) ([]byte, error) {
	if len(data) < 11 {
		return nil, fmt.Errorf("L01 broadcast too short")
	}
	payloadLen := int(binary.BigEndian.Uint16(data[9:11]))
	end := 11 + payloadLen
	if payloadLen < 16 || end > len(data) {
		return nil, fmt.Errorf("invalid L01 broadcast payload")
	}
	encrypted := data[11:end]
	// The reference derives the nonce from sha256(header)[:12] and the key
	// from sha256(token). This path is unused for protocol 1.0 devices.
	_ = encrypted
	return nil, fmt.Errorf("L01 broadcast decode is not implemented")
}

func decryptECB(ciphertext, key []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	if len(ciphertext) == 0 || len(ciphertext)%aes.BlockSize != 0 {
		return nil, fmt.Errorf("ciphertext is not block aligned")
	}
	out := make([]byte, len(ciphertext))
	for off := 0; off < len(ciphertext); off += aes.BlockSize {
		block.Decrypt(out[off:off+aes.BlockSize], ciphertext[off:off+aes.BlockSize])
	}
	return pkcs7Unpad(out, aes.BlockSize)
}
