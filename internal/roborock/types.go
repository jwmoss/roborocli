package roborock

import (
	"crypto/md5"
	"encoding/base64"
	"encoding/json"
)

// Reference holds the Roborock cloud endpoints for one account region.
type Reference struct {
	R string `json:"r,omitempty" yaml:"r,omitempty"`
	A string `json:"a,omitempty" yaml:"a,omitempty"` // API base URL
	M string `json:"m,omitempty" yaml:"m,omitempty"` // MQTT broker URL
	L string `json:"l,omitempty" yaml:"l,omitempty"`
}

// RRiot holds the credentials used to authenticate to the Roborock cloud
// (Hawk) and to the MQTT broker. Field names match the API response.
type RRiot struct {
	U string    `json:"u" yaml:"u"`
	S string    `json:"s" yaml:"s"`
	H string    `json:"h" yaml:"h"`
	K string    `json:"k" yaml:"k"`
	R Reference `json:"r" yaml:"r"`
}

// UserData is the login response stored by the CLI.
type UserData struct {
	RRiot       RRiot  `json:"rriot" yaml:"rriot"`
	UID         int64  `json:"uid,omitempty" yaml:"uid,omitempty"`
	TokenType   string `json:"tokentype,omitempty" yaml:"tokentype,omitempty"`
	Token       string `json:"token,omitempty" yaml:"token,omitempty"`
	RRUID       string `json:"rruid,omitempty" yaml:"rruid,omitempty"`
	Region      string `json:"region,omitempty" yaml:"region,omitempty"`
	CountryCode string `json:"countrycode,omitempty" yaml:"countrycode,omitempty"`
	Country     string `json:"country,omitempty" yaml:"country,omitempty"`
	Nickname    string `json:"nickname,omitempty" yaml:"nickname,omitempty"`
}

// HashedUser returns md5(u:k)[2:10]. The app uses it as both the MQTT topic
// segment and the broker username.
func (r RRiot) HashedUser() string {
	sum := MD5Hex(r.U + ":" + r.K)
	if len(sum) < 10 {
		return sum
	}
	return sum[2:10]
}

// BrokerPassword returns md5(s:k)[16:].
func (r RRiot) BrokerPassword() string {
	sum := MD5Hex(r.S + ":" + r.K)
	if len(sum) <= 16 {
		return sum
	}
	return sum[16:]
}

// SecurityEndpoint returns base64(md5(k)[8:14]). The slice is raw digest
// bytes, not the hex string. The cloud requires it in every MQTT RPC request.
func (r RRiot) SecurityEndpoint() string {
	sum := md5.Sum([]byte(r.K))
	return base64.StdEncoding.EncodeToString(sum[8:14])
}

// Product describes one Roborock product type.
type Product struct {
	ID         string `json:"id"`
	Name       string `json:"name,omitempty"`
	Model      string `json:"model,omitempty"`
	Category   string `json:"category,omitempty"`
	Code       string `json:"code,omitempty"`
	IconURL    string `json:"iconUrl,omitempty"`
	Capability int64  `json:"capability,omitempty"`
}

// UnmarshalJSON accepts the live camelCase home payload and the snake_case
// form used by older clients.
func (d *Device) UnmarshalJSON(data []byte) error {
	type alias Device
	var decoded alias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*d = Device(decoded)
	var extra map[string]json.RawMessage
	if err := json.Unmarshal(data, &extra); err != nil {
		return err
	}
	if d.LocalKey == "" {
		_ = json.Unmarshal(extra["local_key"], &d.LocalKey)
	}
	if d.ProductID == "" {
		_ = json.Unmarshal(extra["product_id"], &d.ProductID)
	}
	if d.TimeZone == "" {
		_ = json.Unmarshal(extra["time_zone_id"], &d.TimeZone)
	}
	if d.FeatureSet == "" {
		_ = json.Unmarshal(extra["feature_set"], &d.FeatureSet)
	}
	return nil
}

// Device is one vacuum (or other device) in the account home.
// The live home API uses camelCase field names.
type Device struct {
	DUID       string `json:"duid"`
	Name       string `json:"name,omitempty"`
	LocalKey   string `json:"localKey,omitempty"`
	ProductID  string `json:"productId,omitempty"`
	FV         string `json:"fv,omitempty"` // firmware version
	PV         string `json:"pv,omitempty"` // protocol version (1.0, A01, B01, L01)
	SN         string `json:"sn,omitempty"` // serial number
	Online     *bool  `json:"online,omitempty"`
	IconURL    string `json:"iconUrl,omitempty"`
	TimeZone   string `json:"timeZoneId,omitempty"`
	FeatureSet string `json:"featureSet,omitempty"`
	SilentOTA  *bool  `json:"silentOtaSwitch,omitempty"`
}

// Room is one room from the home map.
type Room struct {
	ID   int64  `json:"id"`
	Name string `json:"name,omitempty"`
}

// HomeData is the account home payload from the rriot API.
type HomeData struct {
	ID       int64     `json:"id"`
	Name     string    `json:"name,omitempty"`
	Products []Product `json:"products,omitempty"`
	Devices  []Device  `json:"devices,omitempty"`
	Received []Device  `json:"receivedDevices,omitempty"`
	Rooms    []Room    `json:"rooms,omitempty"`
	GeoName  string    `json:"geoName,omitempty"`
}

// AllDevices returns owned and shared devices.
func (h *HomeData) AllDevices() []Device {
	devices := make([]Device, 0, len(h.Devices)+len(h.Received))
	devices = append(devices, h.Devices...)
	devices = append(devices, h.Received...)
	return devices
}

// ProductFor returns the product that matches a device product ID.
func (h *HomeData) ProductFor(device Device) Product {
	for _, p := range h.Products {
		if p.ID == device.ProductID {
			return p
		}
	}
	return Product{}
}

// NetworkInfo is the get_network_info result used to find the local IP.
type NetworkInfo struct {
	IP    string `json:"ip,omitempty"`
	SSID  string `json:"ssid,omitempty"`
	MAC   string `json:"mac,omitempty"`
	BSSID string `json:"bssid,omitempty"`
	RSSI  int    `json:"rssi,omitempty"`
}
