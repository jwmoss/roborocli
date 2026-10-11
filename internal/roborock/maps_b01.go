package roborock

import (
	"bytes"
	"compress/zlib"
	"context"
	"crypto/aes"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"time"
)

func (m *MQTTClient) familyMap(ctx context.Context, duid string) ([]byte, error) {
	m.familyMu.Lock()
	defer m.familyMu.Unlock()
	for len(m.mapUpdates) > 0 {
		<-m.mapUpdates
	}
	switch m.family {
	case "q10":
		if err := m.publishFamily(ctx, duid, map[string]any{"102": map[string]any{}}); err != nil {
			return nil, err
		}
	case "q7":
		raw, err := m.callQ7(ctx, duid, CmdGetMultiMapsList, nil)
		if err != nil {
			return nil, err
		}
		var maps struct {
			Maps []struct {
				ID      int64 `json:"id"`
				Current bool  `json:"cur"`
			} `json:"map_list"`
		}
		if err := json.Unmarshal(raw, &maps); err != nil {
			return nil, err
		}
		if len(maps.Maps) == 0 {
			return nil, fmt.Errorf("device has no saved maps")
		}
		id := maps.Maps[0].ID
		for _, entry := range maps.Maps {
			if entry.Current {
				id = entry.ID
				break
			}
		}
		request := map[string]any{"method": "service.upload_by_mapid", "msgId": strconv.FormatInt(time.Now().UnixMilli(), 10), "params": map[string]int64{"map_id": id}}
		if err := m.publishFamily(ctx, duid, map[string]any{"10000": request}); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("%s devices do not expose vacuum maps", m.family)
	}
	timer := time.NewTimer(m.Timeout)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
			return nil, fmt.Errorf("timeout waiting for %s map", m.family)
		case data := <-m.mapUpdates:
			if m.family == "q7" {
				return decodeQ7Map(data, m.device.SN, m.product.Model)
			}
			if len(data) > 2 && data[0] == 1 && data[1] == 1 {
				return data, nil
			}
		}
	}
}

func decodeQ7Map(data []byte, serial, model string) ([]byte, error) {
	if serial == "" || model == "" {
		return nil, fmt.Errorf("Q7 map requires device serial and model")
	}
	parts := strings.Split(model, ".")
	suffix := parts[len(parts)-1]
	key := []byte((suffix + strings.Repeat("0", 16))[:16])
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	material := pkcs7Pad([]byte(serial+"+"+suffix+"+"+serial), aes.BlockSize)
	for offset := 0; offset < len(material); offset += aes.BlockSize {
		block.Encrypt(material[offset:offset+aes.BlockSize], material[offset:offset+aes.BlockSize])
	}
	mapKey := []byte(MD5Hex(base64.StdEncoding.EncodeToString(material))[8:24])
	blob := strings.TrimSpace(string(data))
	blob += strings.Repeat("=", (4-len(blob)%4)%4)
	encrypted, err := base64.StdEncoding.DecodeString(blob)
	if err != nil {
		return nil, fmt.Errorf("invalid Q7 map base64")
	}
	if len(encrypted) == 0 || len(encrypted)%aes.BlockSize != 0 {
		return nil, fmt.Errorf("Q7 map ciphertext is not block aligned")
	}
	block, err = aes.NewCipher(mapKey)
	if err != nil {
		return nil, err
	}
	for offset := 0; offset < len(encrypted); offset += aes.BlockSize {
		block.Decrypt(encrypted[offset:offset+aes.BlockSize], encrypted[offset:offset+aes.BlockSize])
	}
	plain, err := pkcs7Unpad(encrypted, aes.BlockSize)
	if err != nil {
		return nil, err
	}
	compressed, err := hex.DecodeString(string(plain))
	if err != nil {
		return nil, fmt.Errorf("invalid Q7 map compressed hex")
	}
	reader, err := zlib.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	result, err := io.ReadAll(io.LimitReader(reader, maxMapBytes+1))
	if len(result) > maxMapBytes {
		return nil, fmt.Errorf("Q7 map exceeds maximum size")
	}
	return result, err
}

// Q10 uses raw LZ4 blocks, with explicit bounds on all expansion and offsets.
func decodeLZ4(data []byte, limit int) ([]byte, error) {
	out := make([]byte, 0, min(limit, 1<<20))
	offset := 0
	length := func(value int) (int, error) {
		if value < 15 {
			return value, nil
		}
		for {
			if offset >= len(data) {
				return 0, fmt.Errorf("truncated LZ4 length")
			}
			part := int(data[offset])
			offset++
			value += part
			if value > limit {
				return 0, fmt.Errorf("LZ4 expansion exceeds limit")
			}
			if part != 255 {
				return value, nil
			}
		}
	}
	for offset < len(data) {
		token := data[offset]
		offset++
		literal, err := length(int(token >> 4))
		if err != nil {
			return nil, err
		}
		if literal > len(data)-offset || literal > limit-len(out) {
			return nil, fmt.Errorf("invalid LZ4 literal length")
		}
		out = append(out, data[offset:offset+literal]...)
		offset += literal
		if offset == len(data) {
			return out, nil
		}
		if len(data)-offset < 2 {
			return nil, fmt.Errorf("truncated LZ4 offset")
		}
		distance := int(binary.LittleEndian.Uint16(data[offset:]))
		offset += 2
		if distance == 0 || distance > len(out) {
			return nil, fmt.Errorf("invalid LZ4 back reference")
		}
		count, err := length(int(token & 15))
		if err != nil {
			return nil, err
		}
		count += 4
		if count > limit-len(out) {
			return nil, fmt.Errorf("LZ4 expansion exceeds limit")
		}
		for range count {
			out = append(out, out[len(out)-distance])
		}
	}
	return nil, fmt.Errorf("truncated LZ4 block")
}

func ParseQ10Map(raw []byte) (*MapData, error) {
	if len(raw) < 29 || raw[0] != 1 || raw[1] != 1 {
		return nil, fmt.Errorf("invalid Q10 map header")
	}
	width, height := int(binary.BigEndian.Uint16(raw[7:])), int(binary.BigEndian.Uint16(raw[9:]))
	if width <= 0 || height <= 0 || width > 4096 || height > 4096 {
		return nil, fmt.Errorf("invalid Q10 map dimensions")
	}
	compressed := int(binary.BigEndian.Uint16(raw[27:]))
	if compressed > len(raw)-29 {
		return nil, fmt.Errorf("truncated Q10 map layout")
	}
	data, err := decodeLZ4(raw[29:29+compressed], width*height+2+32*47)
	if err != nil {
		return nil, err
	}
	if len(data) < width*height+2 {
		return nil, fmt.Errorf("truncated Q10 grid")
	}
	resolution := int(binary.BigEndian.Uint16(raw[15:])) * 10
	if resolution <= 0 || resolution > 1000 {
		return nil, fmt.Errorf("invalid Q10 map resolution")
	}
	m := &MapData{Index: binary.BigEndian.Uint32(raw[2:]), Width: width, Height: height, Resolution: resolution, Left: 25500 + int(int16(binary.BigEndian.Uint16(raw[11:])))*5, Bottom: 25500 + int(int16(binary.BigEndian.Uint16(raw[13:])))*5, Rooms: map[int]*MapRoom{}, Pixels: make([]byte, width*height)}
	m.Charger = &MapPoint{25500 + int(int16(binary.BigEndian.Uint16(raw[17:])))*5, 25500 + int(int16(binary.BigEndian.Uint16(raw[19:])))*5}
	for i, value := range data[:width*height] {
		switch {
		case value == 0 || value == 243:
			m.Pixels[i] = 0
		case value == 240:
			m.Pixels[i] = 255
		case value > 240:
			m.Pixels[i] = 1
		default:
			m.Pixels[i] = 255
			id := int(value) / 4
			if id == 0 {
				continue
			}
			m.addRoomPixel(id, i)
		}
	}
	roomData := data[width*height:]
	if roomData[0] != 1 || int(roomData[1])*47+2 != len(roomData) {
		return nil, fmt.Errorf("invalid Q10 room records")
	}
	for i := 2; i < len(roomData); i += 47 {
		r := roomData[i : i+47]
		nameSize := int(r[26])
		if nameSize > 20 {
			return nil, fmt.Errorf("invalid Q10 room name")
		}
		if room := m.Rooms[int(binary.BigEndian.Uint16(r))]; room != nil {
			room.Name = strings.TrimRight(string(r[27:27+nameSize]), "\x00")
		}
	}
	return m, nil
}

type protoField struct {
	number int
	value  uint64
	data   []byte
}

type protoMessage []protoField

func (fields protoMessage) field(number int) protoField {
	for _, field := range fields {
		if field.number == number {
			return field
		}
	}
	return protoField{}
}

// SCMap needs protobuf wire types 0, 1, 2, and 5; nested messages share this parser.
func protoFields(data []byte) (protoMessage, error) {
	var fields protoMessage
	for len(data) > 0 {
		tag, n := binary.Uvarint(data)
		if n <= 0 || tag>>3 == 0 {
			return nil, fmt.Errorf("invalid SCMap field")
		}
		data = data[n:]
		field := protoField{number: int(tag >> 3)}
		switch tag & 7 {
		case 0:
			field.value, n = binary.Uvarint(data)
			if n <= 0 {
				return nil, fmt.Errorf("invalid SCMap varint")
			}
			data = data[n:]
		case 1:
			if len(data) < 8 {
				return nil, fmt.Errorf("truncated SCMap fixed64")
			}
			field.value = binary.LittleEndian.Uint64(data)
			data = data[8:]
		case 5:
			if len(data) < 4 {
				return nil, fmt.Errorf("truncated SCMap float")
			}
			field.value = uint64(binary.LittleEndian.Uint32(data))
			data = data[4:]
		case 2:
			size, n := binary.Uvarint(data)
			if n <= 0 || size > uint64(len(data)-n) {
				return nil, fmt.Errorf("invalid SCMap length")
			}
			field.data = data[n : n+int(size)]
			data = data[n+int(size):]
		default:
			return nil, fmt.Errorf("unsupported SCMap wire type %d", tag&7)
		}
		fields = append(fields, field)
	}
	return fields, nil
}

func ParseQ7Map(raw []byte) (*MapData, error) {
	fields, err := protoFields(raw)
	if err != nil {
		return nil, err
	}
	m := &MapData{Resolution: 50, Rooms: map[int]*MapRoom{}}
	var grid []byte
	for _, field := range fields {
		switch field.number {
		case 3, 4, 7, 8:
		default:
			continue
		}
		data, err := protoFields(field.data)
		if err != nil {
			return nil, err
		}
		switch field.number {
		case 3:
			if err := m.readQ7Header(data); err != nil {
				return nil, err
			}
		case 4:
			grid = data.field(1).data
		case 7, 8:
			xField := 1
			if field.number == 8 {
				xField = 3
			}
			x, err := q7Millimeters(data.field(xField).value)
			if err != nil {
				return nil, err
			}
			y, err := q7Millimeters(data.field(xField + 1).value)
			if err != nil {
				return nil, err
			}
			point := &MapPoint{x, y}
			if field.number == 7 {
				m.Charger = point
			} else {
				m.Robot = point
			}
		}
	}
	if m.Width <= 0 || m.Height <= 0 || m.Width > 4096 || m.Height > 4096 || m.Resolution <= 0 || m.Width*m.Height > len(grid) {
		return nil, fmt.Errorf("invalid Q7 map grid")
	}
	m.Pixels = make([]byte, m.Width*m.Height)
	for i, p := range grid[:len(m.Pixels)] {
		if p == 128 {
			m.Pixels[i] = 1
		} else if p != 0 {
			m.Pixels[i] = 255
		}
	}
	for _, field := range fields {
		if field.number != 14 {
			continue
		}
		if err := m.readQ7Outline(field.data); err != nil {
			return nil, err
		}
	}
	for _, field := range fields {
		if field.number != 12 {
			continue
		}
		values, err := protoFields(field.data)
		if err != nil {
			return nil, err
		}
		if room := m.Rooms[int(values.field(1).value)]; room != nil {
			room.Name = string(values.field(2).data)
		}
	}
	return m, nil
}

func q7Millimeters(bits uint64) (int, error) {
	f := float64(math.Float32frombits(uint32(bits)))
	if math.IsNaN(f) || math.IsInf(f, 0) || math.Abs(f) > 1000 {
		return 0, fmt.Errorf("invalid Q7 map coordinate")
	}
	return int(math.Round(f * 1000)), nil
}

func (m *MapData) readQ7Header(header protoMessage) error {
	m.Index = uint32(header.field(1).value)
	m.Width, m.Height = int(header.field(2).value), int(header.field(3).value)
	for _, field := range []struct {
		number int
		target *int
	}{{4, &m.Left}, {5, &m.Bottom}, {8, &m.Resolution}} {
		value := header.field(field.number)
		if value.number == 0 {
			continue
		}
		millimeters, err := q7Millimeters(value.value)
		if err != nil {
			return err
		}
		*field.target = millimeters
	}
	return nil
}

func (m *MapData) readQ7Outline(raw []byte) error {
	outline, err := protoFields(raw)
	if err != nil {
		return err
	}
	id := int(outline.field(1).value)
	for _, point := range outline {
		if point.number != 2 {
			continue
		}
		values, err := protoFields(point.data)
		if err != nil {
			return err
		}
		x, y := int(values.field(1).value), int(values.field(2).value)
		if x >= 0 && x < m.Width && y >= 0 && y < m.Height {
			m.addRoomPixel(id, y*m.Width+x)
		}
	}
	return nil
}

func (m *MapData) addRoomPixel(id, index int) {
	x, y := m.Left+(index%m.Width)*m.Resolution, m.Bottom+(index/m.Width)*m.Resolution
	r := m.Rooms[id]
	if r == nil {
		r = &MapRoom{ID: id, Bounds: [4]int{x, y, x + m.Resolution, y + m.Resolution}}
		m.Rooms[id] = r
	}
	r.Bounds = [4]int{min(r.Bounds[0], x), min(r.Bounds[1], y), max(r.Bounds[2], x+m.Resolution), max(r.Bounds[3], y+m.Resolution)}
	r.Pixels++
}
