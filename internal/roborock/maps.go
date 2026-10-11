package roborock

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
)

const maxMapBytes = 32 << 20

func (m *MQTTClient) Map(ctx context.Context, duid string, record int64) ([]byte, error) {
	if m.family != "" && m.family != "v1" {
		if record != 0 {
			return nil, fmt.Errorf("record-map retrieval currently requires V1; use the family's native record selector")
		}
		return m.familyMap(ctx, duid)
	}
	method := "get_map_v1"
	var params any
	if record > 0 {
		method = "get_clean_record_map"
		params = []int64{record}
	}
	return m.call(ctx, duid, method, params, true)
}

func (m *MQTTClient) dispatchBlob(msg *Message) {
	data := msg.Payload
	if len(data) < 24 {
		return
	}
	id := uint32(binary.LittleEndian.Uint16(data[16:18]))
	if !bytes.HasPrefix(data, []byte(m.RRiot.SecurityEndpoint())) {
		return
	}
	m.mu.Lock()
	nonce, ok := m.blobs[id]
	ch := m.pending[id]
	if ok {
		delete(m.blobs, id)
		delete(m.pending, id)
	}
	m.mu.Unlock()
	if !ok || ch == nil {
		return
	}
	blob, err := decryptMap(data[24:], nonce)
	ch <- rpcResult{result: blob, err: err}
	close(ch)
}

func decryptMap(body, key []byte) ([]byte, error) {
	if len(body) == 0 || len(body)%aes.BlockSize != 0 {
		return nil, fmt.Errorf("map ciphertext is not block aligned")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	plain := make([]byte, len(body))
	cipher.NewCBCDecrypter(block, make([]byte, aes.BlockSize)).CryptBlocks(plain, body)
	plain, err = pkcs7Unpad(plain, aes.BlockSize)
	if err != nil {
		return nil, err
	}
	reader, err := gzip.NewReader(bytes.NewReader(plain))
	if err != nil {
		return nil, fmt.Errorf("decode map gzip: %w", err)
	}
	defer reader.Close()
	data, err := io.ReadAll(io.LimitReader(reader, maxMapBytes+1))
	if len(data) > maxMapBytes {
		return nil, fmt.Errorf("map exceeds %d bytes", maxMapBytes)
	}
	return data, err
}

type MapPoint struct {
	X int `json:"x_mm"`
	Y int `json:"y_mm"`
}
type MapRoom struct {
	ID     int    `json:"id"`
	Name   string `json:"name,omitempty"`
	Bounds [4]int `json:"bounds_mm"`
	Pixels int    `json:"pixels"`
}
type MapData struct {
	Index      uint32           `json:"index"`
	Sequence   uint32           `json:"sequence"`
	Width      int              `json:"width"`
	Height     int              `json:"height"`
	Left       int              `json:"left_mm"`
	Bottom     int              `json:"bottom_mm"`
	Resolution int              `json:"resolution_mm"`
	Rooms      map[int]*MapRoom `json:"rooms"`
	Robot      *MapPoint        `json:"robot,omitempty"`
	Charger    *MapPoint        `json:"charger,omitempty"`
	Path       []MapPoint       `json:"path,omitempty"`
	Walls      [][4]int         `json:"walls,omitempty"`
	NoGo       [][8]int         `json:"no_go,omitempty"`
	NoMop      [][8]int         `json:"no_mop,omitempty"`
	Pixels     []byte           `json:"-"`
}

// ParseMap decodes the RR block format; each floor pixel covers 50 millimeters.
func ParseMap(raw []byte) (*MapData, error) {
	if len(raw) < 20 || string(raw[:2]) != "rr" {
		return nil, fmt.Errorf("unsupported map header (expected rr)")
	}
	m := &MapData{Index: binary.LittleEndian.Uint32(raw[12:16]), Sequence: binary.LittleEndian.Uint32(raw[16:20]), Rooms: map[int]*MapRoom{}, Resolution: 50}
	offset := int(binary.LittleEndian.Uint16(raw[2:4]))
	if offset < 20 || offset > len(raw) {
		return nil, fmt.Errorf("invalid map header length")
	}
	for offset < len(raw) {
		if len(raw)-offset < 8 {
			return nil, fmt.Errorf("truncated map block")
		}
		kind := binary.LittleEndian.Uint16(raw[offset:])
		header := int(binary.LittleEndian.Uint16(raw[offset+2:]))
		size := int(binary.LittleEndian.Uint32(raw[offset+4:]))
		if header < 8 || header > len(raw)-offset || size > len(raw)-offset-header {
			return nil, fmt.Errorf("invalid map block size")
		}
		h := raw[offset : offset+header]
		data := raw[offset+header : offset+header+size]
		if err := m.readRRBlock(kind, h, data); err != nil {
			return nil, err
		}
		offset += header + size
	}
	if len(m.Pixels) == 0 {
		return nil, fmt.Errorf("map contains no floor image")
	}
	for i, pixel := range m.Pixels {
		if pixel != 255 && pixel != 7 && pixel&7 == 7 {
			m.addRoomPixel(int(pixel>>3), i)
		}
	}
	return m, nil
}

func (m *MapData) readRRBlock(kind uint16, h, data []byte) error {
	switch kind {
	case 1, 8:
		if len(data) < 8 {
			return fmt.Errorf("invalid map position")
		}
		p := &MapPoint{int(binary.LittleEndian.Uint32(data)), int(binary.LittleEndian.Uint32(data[4:]))}
		if kind == 1 {
			m.Charger = p
		} else {
			m.Robot = p
		}
	case 2:
		header := len(h)
		if header < 24 {
			return fmt.Errorf("invalid map image header")
		}
		m.Bottom = int(binary.LittleEndian.Uint32(h[header-16:])) * 50
		m.Left = int(binary.LittleEndian.Uint32(h[header-12:])) * 50
		m.Height = int(binary.LittleEndian.Uint32(h[header-8:]))
		m.Width = int(binary.LittleEndian.Uint32(h[header-4:]))
		if m.Width <= 0 || m.Height <= 0 || m.Width > 4096 || m.Height > 4096 || m.Width*m.Height > len(data) {
			return fmt.Errorf("invalid map image dimensions")
		}
		m.Pixels = bytes.Clone(data[:m.Width*m.Height])
	case 3:
		if len(data)%4 != 0 {
			return fmt.Errorf("invalid map path")
		}
		for i := 0; i < len(data); i += 4 {
			m.Path = append(m.Path, MapPoint{int(binary.LittleEndian.Uint16(data[i:])), int(binary.LittleEndian.Uint16(data[i+2:]))})
		}
	case 10:
		if len(data)%8 != 0 {
			return fmt.Errorf("invalid wall geometry")
		}
		for i := 0; i < len(data); i += 8 {
			var wall [4]int
			for j := range wall {
				wall[j] = int(binary.LittleEndian.Uint16(data[i+2*j:]))
			}
			m.Walls = append(m.Walls, wall)
		}
	case 9, 12:
		if len(data)%16 != 0 {
			return fmt.Errorf("invalid restricted area geometry")
		}
		for i := 0; i < len(data); i += 16 {
			var area [8]int
			for j := range area {
				area[j] = int(binary.LittleEndian.Uint16(data[i+2*j:]))
			}
			if kind == 9 {
				m.NoGo = append(m.NoGo, area)
			} else {
				m.NoMop = append(m.NoMop, area)
			}
		}
	}
	return nil
}

func (m *MapData) PNG(w io.Writer) error {
	img := image.NewRGBA(image.Rect(0, 0, m.Width, m.Height))
	palette := []color.RGBA{{115, 185, 214, 255}, {153, 202, 153, 255}, {231, 184, 123, 255}, {184, 163, 210, 255}, {219, 155, 170, 255}}
	for i, p := range m.Pixels {
		c := color.RGBA{245, 245, 240, 255}
		if p == 0 {
			c = color.RGBA{0, 0, 0, 0}
		} else if p&7 == 1 || p&7 == 0 {
			c = color.RGBA{65, 74, 82, 255}
		} else if p != 255 && p != 7 && p&7 == 7 {
			c = palette[int(p>>3)%len(palette)]
		}
		img.SetRGBA(i%m.Width, m.Height-1-i/m.Width, c)
	}
	point := func(p MapPoint) image.Point {
		return image.Pt((p.X-m.Left)/m.Resolution, m.Height-1-(p.Y-m.Bottom)/m.Resolution)
	}
	for _, p := range m.Path {
		q := point(p)
		img.SetRGBA(q.X, q.Y, color.RGBA{255, 255, 255, 255})
	}
	for i, p := range []*MapPoint{m.Charger, m.Robot} {
		if p == nil {
			continue
		}
		q := point(*p)
		c := color.RGBA{35, 95, 45, 255}
		if i == 1 {
			c = color.RGBA{230, 65, 45, 255}
		}
		for dx := -3; dx <= 3; dx++ {
			for dy := -3; dy <= 3; dy++ {
				if dx*dx+dy*dy <= 9 {
					img.SetRGBA(q.X+dx, q.Y+dy, c)
				}
			}
		}
	}
	return png.Encode(w, img)
}
