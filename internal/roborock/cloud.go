package roborock

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

// LoginBaseURLs are the region gateways used before an account is known.
var LoginBaseURLs = []string{
	"https://usiot.roborock.com",
	"https://euiot.roborock.com",
	"https://cniot.roborock.com",
	"https://ruiot.roborock.com",
}

// AppVersion is the app version header sent during login.
const AppVersion = "4.54.02"

// maxCloudBody bounds every cloud response read.
const maxCloudBody = 8 << 20

// CloudClient talks to the Roborock login API and the authenticated rriot
// API (Hawk-signed requests).
type CloudClient struct {
	HTTP     *http.Client
	Username string
	ClientID string // stable per-install device identifier
	Trace    func(method, path string, status int, duration time.Duration)

	// discovered during login
	baseURL       string
	preferredBase string
	country       string
	countryCode   string
}

// NewCloudClient builds a client. clientID is a stable random 16-character
// string, reused across logins so header_clientid does not change.
func NewCloudClient(username, clientID string, httpClient *http.Client) *CloudClient {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	} else {
		copy := *httpClient
		httpClient = &copy
	}
	// Refuse cross-origin redirects so cloud credentials cannot leak to a
	// host that answers with a redirect.
	previous := httpClient.CheckRedirect
	httpClient.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return fmt.Errorf("stopped after 10 redirects")
		}
		if len(via) > 0 && !sameOrigin(req.URL, via[0].URL) {
			return fmt.Errorf("refusing cross-origin redirect")
		}
		if previous != nil {
			return previous(req, via)
		}
		return nil
	}
	return &CloudClient{HTTP: httpClient, Username: username, ClientID: clientID}
}

// sameOrigin reports whether two URLs share scheme, host, and port.
func sameOrigin(a, b *url.URL) bool {
	if a == nil || b == nil {
		return false
	}
	return strings.EqualFold(a.Scheme, b.Scheme) &&
		strings.EqualFold(a.Hostname(), b.Hostname()) &&
		effectivePort(a) == effectivePort(b)
}

// effectivePort returns the port, normalizing the default for the scheme.
func effectivePort(u *url.URL) string {
	if port := u.Port(); port != "" {
		if normalized := strings.TrimLeft(port, "0"); normalized != "" {
			return normalized
		}
		return "0"
	}
	if strings.EqualFold(u.Scheme, "https") {
		return "443"
	}
	return "80"
}

// SetBaseURL sets the login gateway. Country still comes from getUrlByEmail
// so a configured gateway does not invent a region.
func (c *CloudClient) SetBaseURL(baseURL string) {
	c.preferredBase = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	c.baseURL = ""
	c.country = ""
	c.countryCode = ""
}

// headerClientID derives base64(md5(username + installID)), as the app does.
func (c *CloudClient) headerClientID() string {
	sum := md5.Sum([]byte(c.Username + c.ClientID))
	return base64.StdEncoding.EncodeToString(sum[:])
}

// ClientIDFromSeed derives a stable 16-character install identifier.
func ClientIDFromSeed(seed string) string {
	sum := md5Sum(seed)
	return base64.RawURLEncoding.EncodeToString(sum[:16])[:16]
}

func md5Sum(s string) []byte {
	h := md5.Sum([]byte(s))
	return h[:]
}

// hexMD5 returns the lowercase md5 hex digest.
func hexMD5(b []byte) string { return hex.EncodeToString(md5Sum(string(b))) }

// sortedKV renders "k=v&k=v" with keys sorted, repeating values joined by ",".
func sortedKV(values url.Values) string {
	if len(values) == 0 {
		return ""
	}
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for i, k := range keys {
		if i > 0 {
			b.WriteByte('&')
		}
		b.WriteString(k)
		b.WriteByte('=')
		for j, v := range values[k] {
			if j > 0 {
				b.WriteByte(',')
			}
			b.WriteString(v)
		}
	}
	return b.String()
}

// hawkNonce returns a 16-character hex nonce.
func hawkNonce() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "0000000000000000"
	}
	return hex.EncodeToString(b)
}

// HawkAuthorization builds the Hawk MAC header used by the rriot API:
//
//	Hawk id="<u>", s="<s>", ts="<ts>", nonce="<nonce>", mac="<mac>"
//
// The MAC is HMAC-SHA256, keyed by h, over
// "u:s:nonce:ts:md5(path):md5(sortedQuery):md5(payload)".
// path is the "/a/b" form built from URL segments. payload is md5 of the
// compact JSON body, or of the sorted form string, or empty.
func HawkAuthorization(rriot RRiot, path string, query url.Values, payloadMD5 string) string {
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	nonce := hawkNonce()

	segments := strings.Split(strings.Trim(path, "/"), "/")
	canonicalPath := "/" + strings.Join(segments, "/")
	if strings.Trim(path, "/") == "" {
		canonicalPath = "/"
	}
	// An empty query or payload slot stays empty. md5("") is not the same
	// value, and the server rejects that signature.
	querySlot := ""
	if sorted := sortedKV(query); sorted != "" {
		querySlot = hexMD5([]byte(sorted))
	}
	pathSlot := ""
	if canonicalPath != "/" || strings.Trim(path, "/") != "" {
		pathSlot = hexMD5([]byte(canonicalPath))
	}

	prestr := strings.Join([]string{
		rriot.U,
		rriot.S,
		nonce,
		ts,
		pathSlot,
		querySlot,
		payloadMD5,
	}, ":")
	mac := hmac.New(sha256.New, []byte(rriot.H))
	mac.Write([]byte(prestr))
	macHex := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	return fmt.Sprintf(`Hawk id="%s", s="%s", ts="%s", nonce="%s", mac="%s"`,
		rriot.U, rriot.S, ts, nonce, macHex)
}

// doJSON performs a login-API request and decodes JSON when out is non-nil.
func (c *CloudClient) doJSON(ctx context.Context, base, method, path string, query, form url.Values, extraHeaders map[string]string, out any) error {
	var body io.Reader
	headers := map[string]string{"Accept": "application/json"}
	if form != nil {
		body = strings.NewReader(form.Encode())
		headers["Content-Type"] = "application/x-www-form-urlencoded"
	}
	for k, v := range extraHeaders {
		headers[k] = v
	}
	endpoint := strings.TrimRight(base, "/") + path
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	start := time.Now()
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("request %s: %w", path, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxCloudBody))
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if c.Trace != nil {
		c.Trace(method, path, resp.StatusCode, time.Since(start))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%s: HTTP %d", path, resp.StatusCode)
	}
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("decode %s: %w", path, err)
		}
	}
	return nil
}

// envelope is the common {"code":..,"msg":..,"data":..} response shape.
type envelope struct {
	Code int             `json:"code"`
	Msg  string          `json:"msg"`
	Data json.RawMessage `json:"data"`
}

// BaseURL discovers the login gateway for the account region. It always
// calls getUrlByEmail, because the response carries the country and the
// account's regional gateway that later calls must use.
func (c *CloudClient) BaseURL(ctx context.Context) (string, error) {
	if c.baseURL != "" {
		return c.baseURL, nil
	}
	candidates := LoginBaseURLs
	if c.preferredBase != "" {
		candidates = []string{c.preferredBase}
		// Fall back to the regional gateways only when the caller opts in,
		// so tests and offline use never reach the live service.
		if os.Getenv("ROBOROCLI_DISCOVER_REGIONS") == "1" {
			candidates = append(candidates, LoginBaseURLs...)
		}
	}
	var lastErr error
	for _, base := range candidates {
		var env envelope
		q := url.Values{"email": {c.Username}, "needtwostepauth": {"false"}}
		err := c.doJSON(ctx, base, http.MethodPost, "/api/v1/getUrlByEmail", q, nil, nil, &env)
		if err != nil {
			lastErr = err
			continue
		}
		if env.Code != 200 {
			lastErr = fmt.Errorf("getUrlByEmail code %d: %s", env.Code, env.Msg)
			if os.Getenv("ROBOROCLI_DEBUG_LOGIN") == "1" {
				fmt.Fprintf(os.Stderr, "debug: getUrlByEmail %s -> code %d msg %q\n", base, env.Code, env.Msg)
			}
			continue
		}
		var data struct {
			URL         string `json:"url"`
			Country     string `json:"country"`
			CountryCode string `json:"countrycode"`
		}
		if err := json.Unmarshal(env.Data, &data); err != nil {
			lastErr = err
			continue
		}
		// A gateway that does not know the account returns an empty country.
		// Keep trying the other regional gateways in that case.
		if data.Country == "" && data.CountryCode == "" {
			lastErr = fmt.Errorf("gateway %s did not resolve an account region", base)
			continue
		}
		// Prefer the gateway the server reports for this account.
		c.baseURL = data.URL
		if c.baseURL == "" {
			c.baseURL = base
		}
		c.country = data.Country
		c.countryCode = data.CountryCode
		return c.baseURL, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no gateway responded")
	}
	return "", fmt.Errorf("discover login gateway: %w", lastErr)
}

// Country returns the discovered country name and dial code.
func (c *CloudClient) Country(ctx context.Context) (string, string) {
	if _, err := c.BaseURL(ctx); err != nil {
		return "", ""
	}
	return c.country, c.countryCode
}

// SendEmailCode requests a login code by email. It prefers the v4 endpoint
// and falls back to v1.
func (c *CloudClient) SendEmailCode(ctx context.Context) error {
	base, err := c.BaseURL(ctx)
	if err != nil {
		return err
	}
	country, countryCode := c.country, c.countryCode
	var v4Err error
	if country != "" && countryCode != "" {
		headers := map[string]string{
			"header_clientid":   c.headerClientID(),
			"header_clientlang": "en",
		}
		form := url.Values{"email": {c.Username}, "type": {"login"}, "platform": {""}}
		var env envelope
		err = c.doJSON(ctx, base, http.MethodPost, "/api/v4/email/code/send", nil, form, headers, &env)
		if err == nil && env.Code == 200 {
			return nil
		}
		if err == nil {
			if env.Code == 2008 {
				return fmt.Errorf("account does not exist: %s", c.Username)
			}
			if env.Code == 9002 {
				return fmt.Errorf("too many code requests; try again later")
			}
			v4Err = fmt.Errorf("code send code %d: %s", env.Code, env.Msg)
		} else {
			v4Err = err
		}
	}
	// v1 fallback
	headers := map[string]string{"header_clientid": c.headerClientID()}
	q := url.Values{"username": {c.Username}, "type": {"auth"}}
	var env envelope
	err = c.doJSON(ctx, base, http.MethodPost, "/api/v1/sendEmailCode", q, nil, headers, &env)
	if err != nil {
		if v4Err != nil {
			return fmt.Errorf("v4: %v; v1: %w", v4Err, err)
		}
		return err
	}
	if env.Code != 200 {
		return fmt.Errorf("code send code %d: %s", env.Code, env.Msg)
	}
	return nil
}

// SignKey signs a random challenge string for the login payload headers.
func (c *CloudClient) SignKey(ctx context.Context, challenge string) (string, error) {
	base, err := c.BaseURL(ctx)
	if err != nil {
		return "", err
	}
	headers := map[string]string{"header_clientid": c.headerClientID()}
	var env envelope
	err = c.doJSON(ctx, base, http.MethodPost, "/api/v3/key/sign",
		url.Values{"s": {challenge}}, nil, headers, &env)
	if err != nil {
		return "", err
	}
	if env.Code != 200 {
		return "", fmt.Errorf("key sign code %d: %s", env.Code, env.Msg)
	}
	var data struct {
		K string `json:"k"`
	}
	if err := json.Unmarshal(env.Data, &data); err != nil {
		return "", err
	}
	if data.K == "" {
		return "", fmt.Errorf("key sign returned empty key")
	}
	return data.K, nil
}

// agreementVersion fetches the current user agreement version, falling back
// to the default that login accepts.
func (c *CloudClient) agreementVersion(ctx context.Context, country string) (int, int) {
	base, err := c.BaseURL(ctx)
	if err != nil {
		return 14, 0
	}
	var env envelope
	headers := map[string]string{"header_clientlang": "en"}
	err = c.doJSON(ctx, base, http.MethodGet, "/api/v3/app/agreement/latest",
		url.Values{"country": {country}}, nil, headers, &env)
	if err == nil && env.Code == 200 {
		var data struct {
			Major int `json:"majorVersion"`
			Minor int `json:"minorVersion"`
		}
		if json.Unmarshal(env.Data, &data) == nil {
			return data.Major, data.Minor
		}
	}
	return 14, 0
}

// RandomChallenge returns a 16-character alphanumeric challenge string.
func RandomChallenge() string {
	const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, 16)
	max := big.NewInt(int64(len(alphabet)))
	for i := range b {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			b[i] = alphabet[i%len(alphabet)]
			continue
		}
		b[i] = alphabet[n.Int64()]
	}
	return string(b)
}

// Login exchanges an email login code for user data. It uses the v4 flow and
// falls back to v1 loginWithCode.
func (c *CloudClient) Login(ctx context.Context, code string) (*UserData, error) {
	base, err := c.BaseURL(ctx)
	if err != nil {
		return nil, err
	}
	country, countryCode := c.country, c.countryCode
	if country != "" && countryCode != "" {
		if user, err := c.loginV4(ctx, base, code, country, countryCode); err == nil {
			return user, nil
		} else if user, verr := c.loginV1(ctx, base, code); verr == nil {
			return user, nil
		} else {
			return nil, fmt.Errorf("login failed (v4: %v; v1: %w)", err, verr)
		}
	}
	return c.loginV1(ctx, base, code)
}

func (c *CloudClient) loginV4(ctx context.Context, base, code, country, countryCode string) (*UserData, error) {
	challenge := RandomChallenge()
	signed, err := c.SignKey(ctx, challenge)
	if err != nil {
		return nil, err
	}
	major, minor := c.agreementVersion(ctx, country)
	headers := map[string]string{
		"header_clientid":    c.headerClientID(),
		"x-mercy-ks":         challenge,
		"x-mercy-k":          signed,
		"header_clientlang":  "en",
		"header_appversion":  AppVersion,
		"header_phonesystem": "iOS",
		"header_phonemodel":  "iPhone16,1",
	}
	form := url.Values{
		"country":      {country},
		"countryCode":  {countryCode},
		"email":        {c.Username},
		"code":         {code},
		"majorVersion": {strconv.Itoa(major)},
		"minorVersion": {strconv.Itoa(minor)},
	}
	var env envelope
	err = c.doJSON(ctx, base, http.MethodPost, "/api/v4/auth/email/login/code", nil, form, headers, &env)
	if err != nil {
		return nil, err
	}
	switch env.Code {
	case 200:
		var user UserData
		if err := json.Unmarshal(env.Data, &user); err != nil {
			return nil, err
		}
		return &user, nil
	case 2018:
		return nil, fmt.Errorf("invalid code")
	case 3009:
		return nil, fmt.Errorf("accept the user agreement in the Roborock app first")
	case 3006:
		return nil, fmt.Errorf("stale user agreement; accept it in the Roborock app first")
	case 3039, 2008:
		return nil, fmt.Errorf("account does not exist")
	default:
		return nil, fmt.Errorf("code %d: %s", env.Code, env.Msg)
	}
}

func (c *CloudClient) loginV1(ctx context.Context, base, code string) (*UserData, error) {
	headers := map[string]string{"header_clientid": c.headerClientID()}
	q := url.Values{
		"username":       {c.Username},
		"verifycode":     {code},
		"verifycodetype": {"AUTH_EMAIL_CODE"},
	}
	var env envelope
	err := c.doJSON(ctx, base, http.MethodPost, "/api/v1/loginWithCode", q, nil, headers, &env)
	if err != nil {
		return nil, err
	}
	if env.Code != 200 {
		if env.Code == 2018 {
			return nil, fmt.Errorf("invalid code")
		}
		return nil, fmt.Errorf("code %d: %s", env.Code, env.Msg)
	}
	var user UserData
	if err := json.Unmarshal(env.Data, &user); err != nil {
		return nil, err
	}
	return &user, nil
}

// iotRequest performs a Hawk-signed request against the rriot API and
// decodes the {"success":..,"result":..} envelope into out.
func (c *CloudClient) iotRequest(ctx context.Context, rriot RRiot, method, path string, query, form url.Values, jsonBody any, out any) error {
	base := rriot.R.A
	if base == "" {
		return fmt.Errorf("missing API base URL; run roborocli login")
	}
	var (
		body    io.Reader
		payload string
	)
	headers := map[string]string{"Accept": "application/json"}
	switch {
	case jsonBody != nil:
		raw, err := json.Marshal(jsonBody)
		if err != nil {
			return err
		}
		body = strings.NewReader(string(raw))
		headers["Content-Type"] = "application/json"
		payload = hexMD5(raw)
	case form != nil:
		body = strings.NewReader(form.Encode())
		headers["Content-Type"] = "application/x-www-form-urlencoded"
		payload = hexMD5([]byte(sortedKV(form)))
	}
	headers["Authorization"] = HawkAuthorization(rriot, path, query, payload)
	headers["X-IOTSDK-VERSION"] = "1.0.1"

	endpoint := strings.TrimRight(base, "/") + path
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	start := time.Now()
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("request %s: %w", path, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxCloudBody))
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if c.Trace != nil {
		c.Trace(method, path, resp.StatusCode, time.Since(start))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%s: HTTP %d", path, resp.StatusCode)
	}
	var env struct {
		Success *bool           `json:"success"`
		Code    int             `json:"code"`
		Msg     string          `json:"msg"`
		Result  json.RawMessage `json:"result"`
		Extra   map[string]any  `json:"-"`
		_       struct{}        `json:"-"`
	}
	if err := json.Unmarshal(data, &env); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	if env.Success != nil && !*env.Success {
		msg := env.Msg
		if env.Code != 0 {
			msg = fmt.Sprintf("%s (code %d)", msg, env.Code)
		}
		return fmt.Errorf("%s: %s", path, strings.TrimSpace(msg))
	}
	if out != nil && len(env.Result) > 0 {
		if err := json.Unmarshal(env.Result, out); err != nil {
			return fmt.Errorf("decode %s result: %w", path, err)
		}
	}
	return nil
}

// RawRequest performs a Hawk-signed request and returns the whole response
// body, so scripts can read fields outside "result". A JSON request body is
// sent byte for byte, which keeps large numbers exact.
func (c *CloudClient) RawRequest(ctx context.Context, user UserData, method, path string, query, form url.Values, jsonBody json.RawMessage, out *json.RawMessage) error {
	base := user.RRiot.R.A
	if base == "" {
		return fmt.Errorf("missing API base URL; run roborocli login")
	}
	var (
		body    io.Reader
		payload string
	)
	headers := map[string]string{"Accept": "application/json"}
	switch {
	case len(jsonBody) > 0:
		body = bytes.NewReader(jsonBody)
		headers["Content-Type"] = "application/json"
		payload = hexMD5(jsonBody)
	case form != nil:
		body = strings.NewReader(form.Encode())
		headers["Content-Type"] = "application/x-www-form-urlencoded"
		payload = hexMD5([]byte(sortedKV(form)))
	}
	headers["Authorization"] = HawkAuthorization(user.RRiot, path, query, payload)
	headers["X-IOTSDK-VERSION"] = "1.0.1"

	endpoint := strings.TrimRight(base, "/") + path
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	start := time.Now()
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("request %s: %w", path, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxCloudBody))
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if c.Trace != nil {
		c.Trace(method, path, resp.StatusCode, time.Since(start))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%s: HTTP %d", path, resp.StatusCode)
	}
	if out != nil {
		*out = json.RawMessage(data)
	}
	return nil
}

// HomeID fetches the account home identifier used by the rriot API.
func (c *CloudClient) HomeID(ctx context.Context, user UserData) (int64, error) {
	// Use the gateway stored at login: getUrlByEmail is rate limited, and
	// only login needs the country it returns.
	base := c.preferredBase
	if base == "" {
		var err error
		if base, err = c.BaseURL(ctx); err != nil {
			return 0, err
		}
	}
	headers := map[string]string{"header_clientid": c.headerClientID()}
	if user.Token != "" {
		headers["Authorization"] = user.Token
	}
	var env envelope
	err := c.doJSON(ctx, base, http.MethodGet, "/api/v1/getHomeDetail", nil, nil, headers, &env)
	if err != nil {
		return 0, err
	}
	if env.Code != 200 {
		return 0, fmt.Errorf("getHomeDetail code %d: %s", env.Code, env.Msg)
	}
	var data struct {
		RRHomeID json.RawMessage `json:"rrHomeId"`
	}
	if err := json.Unmarshal(env.Data, &data); err != nil {
		return 0, err
	}
	// rrHomeId is sometimes a JSON number, sometimes a quoted string.
	if len(data.RRHomeID) > 0 && data.RRHomeID[0] == '"' {
		var s string
		json.Unmarshal(data.RRHomeID, &s)
		return strconv.ParseInt(s, 10, 64)
	}
	return strconv.ParseInt(string(data.RRHomeID), 10, 64)
}

// HomeData fetches the home payload with devices, products, and rooms.
func (c *CloudClient) HomeData(ctx context.Context, user UserData, homeID int64) (*HomeData, error) {
	// Prefer v4, which the current app uses; fall back to v3.
	for _, prefix := range []string{"/v4", "/v3", ""} {
		var home HomeData
		err := c.iotRequest(ctx, user.RRiot, http.MethodGet,
			fmt.Sprintf("%s/user/homes/%d", prefix, homeID), nil, nil, nil, &home)
		if err == nil {
			return &home, nil
		}
	}
	return nil, fmt.Errorf("fetch home data")
}

// DeviceShadow fetches the cloud-reported state for a device.
func (c *CloudClient) DeviceShadow(ctx context.Context, rriot RRiot, duid string) (json.RawMessage, error) {
	var out json.RawMessage
	err := c.iotRequest(ctx, rriot, http.MethodGet,
		fmt.Sprintf("/devices/%s/shadow", url.PathEscape(duid)), nil, nil, nil, &out)
	return out, err
}

type Routine struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Enabled *bool  `json:"enabled,omitempty"`
}

func (c *CloudClient) Routines(ctx context.Context, rriot RRiot, duid string) ([]Routine, error) {
	var routines []Routine
	err := c.iotRequest(ctx, rriot, http.MethodGet, "/user/scene/device/"+url.PathEscape(duid), nil, nil, nil, &routines)
	if routines == nil {
		routines = []Routine{}
	}
	return routines, err
}

func (c *CloudClient) ExecuteRoutine(ctx context.Context, rriot RRiot, id int64) error {
	return c.iotRequest(ctx, rriot, http.MethodPost, fmt.Sprintf("/user/scene/%d/execute", id), nil, nil, nil, nil)
}
