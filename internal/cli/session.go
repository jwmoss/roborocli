package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jwmoss/roborocli/internal/config"
	"github.com/jwmoss/roborocli/internal/roborock"
)

// Caller sends an RPC method to a device and returns its result.
type Caller interface {
	Call(ctx context.Context, method string, params any) (json.RawMessage, error)
	Transport() string
	Close()
}

// deviceSession bundles a device with the account data needed to reach it.
type deviceSession struct {
	Cloud   *roborock.CloudClient
	User    *roborock.UserData
	Home    *roborock.HomeData
	HomeID  int64
	Device  roborock.Device
	Product roborock.Product
}

// configPath returns the effective config path.
func (rc *runtime) configPath() string {
	if rc.g.configPath != "" {
		return rc.g.configPath
	}
	return config.DefaultPath()
}

// loadConfig reads the config with flag overrides applied.
func (rc *runtime) loadConfig() (*config.Config, error) {
	cfg, err := config.Load(rc.configPath())
	if err != nil {
		return nil, err
	}
	if rc.g.baseURL != "" {
		cfg.BaseURL = rc.g.baseURL
	}
	return cfg, nil
}

// cloudClient builds a cloud client for the stored account.
func (rc *runtime) cloudClient(cfg *config.Config) *roborock.CloudClient {
	client := roborock.NewCloudClient(cfg.Username, cfg.ClientID, nil)
	if cfg.BaseURL != "" {
		client.SetBaseURL(cfg.BaseURL)
	}
	if rc.g.traceHTTP {
		client.Trace = func(method, path string, status int, d time.Duration) {
			rc.out.Errorf("[cloud] %s %s -> %d (%s)", method, path, status, d)
		}
	}
	return client
}

// openSession loads credentials, fetches the home payload, and selects the
// requested device.
func (rc *runtime) openSession(ctx context.Context) (*deviceSession, error) {
	cfg, err := rc.loadConfig()
	if err != nil {
		return nil, err
	}
	if err := cfg.ValidateAuth(); err != nil {
		return nil, err
	}
	rc.cfg = cfg

	cloud := rc.cloudClient(cfg)
	user := cfg.UserData
	homeID, err := cloud.HomeID(ctx, *user)
	if err != nil {
		return nil, fmt.Errorf("fetch home id: %w", err)
	}
	home, err := cloud.HomeData(ctx, *user, homeID)
	if err != nil {
		return nil, fmt.Errorf("fetch home data: %w", err)
	}
	device, err := selectDevice(home, rc.g.device, cfg.DefaultDUID)
	if err != nil {
		return nil, err
	}
	return &deviceSession{
		Cloud:   cloud,
		User:    user,
		Home:    home,
		HomeID:  homeID,
		Device:  device,
		Product: home.ProductFor(device),
	}, nil
}

// selectDevice resolves the device by explicit flag, config default, or by
// requiring an unambiguous match.
func selectDevice(home *roborock.HomeData, requested, fallback string) (roborock.Device, error) {
	devices := home.AllDevices()
	if len(devices) == 0 {
		return roborock.Device{}, fmt.Errorf("no devices found in this account")
	}
	wanted := strings.TrimSpace(requested)
	if wanted == "" {
		wanted = strings.TrimSpace(fallback)
	}
	if wanted != "" {
		for _, d := range devices {
			if d.DUID == wanted || d.Name == wanted {
				return d, nil
			}
		}
		// Allow a unique duid prefix.
		var matches []roborock.Device
		for _, d := range devices {
			if strings.HasPrefix(d.DUID, wanted) {
				matches = append(matches, d)
			}
		}
		if len(matches) == 1 {
			return matches[0], nil
		}
		if len(matches) > 1 {
			return roborock.Device{}, fmt.Errorf("device prefix %q matches %d devices", wanted, len(matches))
		}
		return roborock.Device{}, fmt.Errorf("device %q not found; run: roborocli devices", wanted)
	}
	if len(devices) == 1 {
		return devices[0], nil
	}
	names := make([]string, 0, len(devices))
	for _, d := range devices {
		names = append(names, fmt.Sprintf("%s (%s)", d.Name, d.DUID))
	}
	return roborock.Device{}, fmt.Errorf("several devices found; select one with --device: %s",
		strings.Join(names, ", "))
}

// deviceHost returns a known local IP for the device from flags or cache.
func (rc *runtime) deviceHost(cfg *config.Config, duid string) string {
	if rc.g.host != "" {
		return rc.g.host
	}
	if cfg.DeviceIPs != nil {
		return cfg.DeviceIPs[duid]
	}
	return ""
}

// resolveLocalIP asks the cloud (over MQTT) for the device's local IP and
// caches it in the config.
func (rc *runtime) resolveLocalIP(ctx context.Context, cfg *config.Config, session *deviceSession) (string, error) {
	if ip := rc.deviceHost(cfg, session.Device.DUID); ip != "" {
		return ip, nil
	}
	if session.Device.LocalKey == "" {
		return "", fmt.Errorf("device has no local key; cannot reach it over MQTT")
	}
	mqttClient := roborock.NewMQTTClient(session.User.RRiot)
	mqttClient.SetDeviceKey(session.Device.LocalKey)
	if err := mqttClient.Connect(ctx, session.Device.DUID); err != nil {
		return "", err
	}
	defer mqttClient.Close()

	result, err := mqttClient.Call(ctx, session.Device.DUID, roborock.CmdGetNetworkInfo, nil)
	if err != nil {
		return "", err
	}
	var info roborock.NetworkInfo
	if err := json.Unmarshal(result, &info); err != nil {
		return "", fmt.Errorf("decode network info: %w", err)
	}
	if info.IP == "" {
		return "", fmt.Errorf("device reported no local IP")
	}
	if !rc.g.dryRun && info.IP != "0.0.0.0" {
		next := *cfg
		if next.DeviceIPs == nil {
			next.DeviceIPs = map[string]string{}
		}
		next.DeviceIPs[session.Device.DUID] = info.IP
		if err := config.Save(rc.configPath(), next, true); err != nil {
			rc.out.Errorf("cache device IP: %v", err)
		}
	}
	return info.IP, nil
}

// openCaller builds the transport for a device. It prefers the local TCP
// connection when the host is known or resolvable, and falls back to MQTT.
func (rc *runtime) openCaller(ctx context.Context, session *deviceSession) (Caller, error) {
	cfg := rc.cfg
	host := rc.deviceHost(cfg, session.Device.DUID)

	// An explicit --host or --mqtt forces the transport choice.
	if !rc.g.mqtt {
		if host == "" && session.Device.LocalKey != "" {
			resolved, err := rc.resolveLocalIP(ctx, cfg, session)
			if err == nil {
				host = resolved
			} else if rc.g.host != "" {
				return nil, err
			}
		}
		if host != "" && session.Device.LocalKey != "" {
			local := &roborock.LocalClient{
				Host:     host,
				LocalKey: session.Device.LocalKey,
				DUID:     session.Device.DUID,
				Timeout:  rc.g.timeout,
			}
			if err := local.Connect(ctx); err != nil {
				if rc.g.host != "" {
					return nil, fmt.Errorf("local connect to %s: %w", host, err)
				}
				rc.out.Errorf("local connect failed (%v); falling back to MQTT", err)
			} else {
				return &localCaller{client: local}, nil
			}
		}
	}

	if session.Device.LocalKey == "" {
		return nil, fmt.Errorf("device has no local key; MQTT control unavailable")
	}
	mqttClient := roborock.NewMQTTClient(session.User.RRiot)
	mqttClient.Timeout = rc.g.timeout
	mqttClient.SetDeviceKey(session.Device.LocalKey)
	if err := mqttClient.Connect(ctx, session.Device.DUID); err != nil {
		return nil, err
	}
	return &mqttCaller{client: mqttClient, duid: session.Device.DUID}, nil
}

// localCaller adapts LocalClient to the Caller interface.
type localCaller struct{ client *roborock.LocalClient }

func (l *localCaller) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	return l.client.Call(ctx, method, params)
}
func (l *localCaller) Transport() string { return "local" }
func (l *localCaller) Close()            { _ = l.client.Close() }

// mqttCaller adapts MQTTClient to the Caller interface.
type mqttCaller struct {
	client *roborock.MQTTClient
	duid   string
}

func (m *mqttCaller) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	return m.client.Call(ctx, m.duid, method, params)
}
func (m *mqttCaller) Transport() string { return "mqtt" }
func (m *mqttCaller) Close()            { m.client.Close() }

// withDevice resolves the device session and caller, runs fn, then closes.
func (rc *runtime) withDevice(ctx context.Context, fn func(session *deviceSession, caller Caller) error) error {
	session, err := rc.openSession(ctx)
	if err != nil {
		return err
	}
	caller, err := rc.openCaller(ctx, session)
	if err != nil {
		return err
	}
	defer caller.Close()
	return fn(session, caller)
}

// configSave writes the config, keeping private permissions.
func configSave(rc *runtime, cfg config.Config) error {
	return config.Save(rc.configPath(), cfg, true)
}
