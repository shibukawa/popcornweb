package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/shibukawa/popcornweb/contrib/internal/authn"
)

const (
	defaultDeviceInterval = int64(5)
	maxDeviceInterval     = int64(3600)
	maxDeviceLifetime     = int64(24 * 60 * 60)
	maxDeviceValueBytes   = 16 << 10
)

func NewDeviceClient(config DeviceConfig, options DeviceOptions) (*DeviceClient, error) {
	if config.DeviceAuthorizationEndpoint == "" || config.TokenEndpoint == "" ||
		config.ClientID == "" || len(config.ClientID) > maxClientValueBytes ||
		len(config.ClientSecret) > maxClientValueBytes {
		return nil, ErrInvalidConfig
	}
	if config.AuthMethod == "" {
		if config.ClientSecret == "" {
			config.AuthMethod = AuthNone
		} else {
			config.AuthMethod = AuthBasic
		}
	}
	if config.AuthMethod != AuthNone && config.AuthMethod != AuthBasic && config.AuthMethod != AuthPost {
		return nil, ErrInvalidConfig
	}
	if (config.AuthMethod == AuthNone) != (config.ClientSecret == "") {
		return nil, ErrInvalidConfig
	}
	deviceEndpoint, err := authn.ParseEndpoint(config.DeviceAuthorizationEndpoint, config.AllowLoopbackHTTP)
	if err != nil {
		return nil, ErrInvalidConfig
	}
	tokenEndpoint, err := authn.ParseEndpoint(config.TokenEndpoint, config.AllowLoopbackHTTP)
	if err != nil {
		return nil, ErrInvalidConfig
	}
	if config.EndpointValidator != nil {
		for _, endpoint := range []*url.URL{deviceEndpoint, tokenEndpoint} {
			candidate := *endpoint
			if config.EndpointValidator(&candidate) != nil {
				return nil, ErrInvalidConfig
			}
		}
	}
	if options.MaxResponseBytes < 0 || options.MaxResponseBytes > maxMaxResponseBytes ||
		options.RequestTimeout < 0 || options.RequestTimeout > maxRequestTimeout {
		return nil, ErrInvalidOptions
	}
	clock := options.Clock
	if clock == nil {
		clock = time.Now
	}
	wait := options.Wait
	if wait == nil {
		wait = waitContext
	}
	client := options.HTTPClient
	if client == nil {
		client = &http.Client{}
	} else {
		copy := *client
		client = &copy
	}
	client.CheckRedirect = authn.RejectRedirect
	// The same reason NewClient wraps its client: a device flow polls a token
	// endpoint from whatever is driving it, and on TinyGo a context deadline
	// bounds nothing on its own.
	client = authn.EnforceDeadlines(client)
	maxResponse := options.MaxResponseBytes
	if maxResponse == 0 {
		maxResponse = defaultMaxResponseBytes
	}
	requestTimeout := options.RequestTimeout
	if requestTimeout == 0 {
		requestTimeout = defaultRequestTimeout
	}
	return &DeviceClient{config: config, clock: clock, wait: wait, maxResponse: maxResponse, requestTimeout: requestTimeout, httpClient: client}, nil
}

func (c *DeviceClient) Begin(ctx context.Context, options DeviceBeginOptions) (DeviceAuthorization, error) {
	if c == nil || ctx == nil || len(options.Scopes) > 64 {
		return DeviceAuthorization{}, ErrInvalidOptions
	}
	for _, scope := range options.Scopes {
		if !validScope(scope) {
			return DeviceAuthorization{}, ErrInvalidOptions
		}
	}
	form := url.Values{"client_id": {c.config.ClientID}}
	if len(options.Scopes) > 0 {
		form.Set("scope", strings.Join(options.Scopes, " "))
	}
	body, status, err := c.post(ctx, c.config.DeviceAuthorizationEndpoint, form)
	if err != nil {
		return DeviceAuthorization{}, err
	}
	if status < 200 || status >= 300 {
		return DeviceAuthorization{}, parseDeviceError(body, status)
	}
	return c.parseAuthorization(body)
}

func (c *DeviceClient) Poll(ctx context.Context, authorization DeviceAuthorization) (TokenSet, error) {
	if c == nil || ctx == nil || !validDeviceAuthorization(authorization, c.clock()) {
		return TokenSet{}, ErrInvalidOptions
	}
	interval := time.Duration(authorization.Interval) * time.Second
	for {
		if !c.clock().Before(authorization.ExpiresAt) {
			return TokenSet{}, ErrExpired
		}
		if err := c.wait(ctx, interval); err != nil {
			return TokenSet{}, err
		}
		if !c.clock().Before(authorization.ExpiresAt) {
			return TokenSet{}, ErrExpired
		}
		form := url.Values{
			"grant_type":  {DeviceGrantType},
			"device_code": {authorization.deviceCode},
			"client_id":   {c.config.ClientID},
		}
		body, status, err := c.post(ctx, c.config.TokenEndpoint, form)
		if err != nil {
			if !errors.Is(err, ErrHTTP) {
				return TokenSet{}, err
			}
			if ctx.Err() != nil {
				return TokenSet{}, ctx.Err()
			}
			if interval < time.Duration(maxDeviceInterval)*time.Second/2 {
				interval *= 2
			} else {
				interval = time.Duration(maxDeviceInterval) * time.Second
			}
			continue
		}
		if status >= 200 && status < 300 {
			return parseTokenSet(body, c.maxResponse)
		}
		deviceErr := parseDeviceError(body, status)
		var protocolErr *DeviceError
		if !errors.As(deviceErr, &protocolErr) {
			return TokenSet{}, deviceErr
		}
		switch protocolErr.Code {
		case "authorization_pending":
			continue
		case "slow_down":
			interval += 5 * time.Second
			if interval > time.Duration(maxDeviceInterval)*time.Second {
				interval = time.Duration(maxDeviceInterval) * time.Second
			}
		case "access_denied":
			return TokenSet{}, ErrAccessDenied
		case "expired_token":
			return TokenSet{}, ErrExpired
		default:
			return TokenSet{}, protocolErr
		}
	}
}

func (c *DeviceClient) post(ctx context.Context, endpoint string, form url.Values) ([]byte, int, error) {
	requestCtx := ctx
	var cancel context.CancelFunc
	if c.requestTimeout > 0 {
		requestCtx, cancel = context.WithTimeout(ctx, c.requestTimeout)
		defer cancel()
	}
	if c.config.AuthMethod == AuthPost {
		form.Set("client_secret", c.config.ClientSecret)
	}
	req, err := http.NewRequestWithContext(requestCtx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, 0, ErrHTTP
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	if c.config.AuthMethod == AuthBasic {
		req.SetBasicAuth(c.config.ClientID, c.config.ClientSecret)
	}
	response, err := c.httpClient.Do(req)
	if err != nil {
		return nil, 0, ErrHTTP
	}
	defer response.Body.Close()
	body, err := authn.ReadBounded(response.Body, int64(c.maxResponse))
	if err != nil {
		if errors.Is(err, authn.ErrLimitExceeded) {
			return nil, response.StatusCode, ErrLimitExceeded
		}
		return nil, response.StatusCode, ErrHTTP
	}
	return body, response.StatusCode, nil
}

func (c *DeviceClient) parseAuthorization(body []byte) (DeviceAuthorization, error) {
	if err := authn.ValidateJSON(body, authn.JSONOptions{MaxBytes: c.maxResponse, MaxDepth: 4, MaxMembers: 32}); err != nil {
		if errors.Is(err, authn.ErrLimitExceeded) {
			return DeviceAuthorization{}, ErrLimitExceeded
		}
		return DeviceAuthorization{}, ErrMalformed
	}
	var raw map[string]json.RawMessage
	if json.Unmarshal(body, &raw) != nil || raw == nil {
		return DeviceAuthorization{}, ErrMalformed
	}
	var result DeviceAuthorization
	if decodeRequiredString(raw, "device_code", &result.deviceCode) != nil ||
		decodeRequiredString(raw, "user_code", &result.UserCode) != nil ||
		decodeRequiredString(raw, "verification_uri", &result.VerificationURI) != nil ||
		decodeOptionalString(raw, "verification_uri_complete", &result.VerificationURIComplete) != nil ||
		len(result.deviceCode) > maxDeviceValueBytes || len(result.UserCode) > 256 {
		return DeviceAuthorization{}, ErrMalformed
	}
	if json.Unmarshal(raw["expires_in"], &result.ExpiresIn) != nil || result.ExpiresIn <= 0 || result.ExpiresIn > maxDeviceLifetime {
		return DeviceAuthorization{}, ErrMalformed
	}
	result.Interval = defaultDeviceInterval
	if value, ok := raw["interval"]; ok {
		if json.Unmarshal(value, &result.Interval) != nil || result.Interval <= 0 || result.Interval > maxDeviceInterval {
			return DeviceAuthorization{}, ErrMalformed
		}
	}
	if !c.validVerificationURI(result.VerificationURI) ||
		(result.VerificationURIComplete != "" && !c.validVerificationURI(result.VerificationURIComplete)) {
		return DeviceAuthorization{}, ErrMalformed
	}
	result.ExpiresAt = c.clock().Add(time.Duration(result.ExpiresIn) * time.Second)
	return result, nil
}

func (c *DeviceClient) validVerificationURI(raw string) bool {
	parsed, err := authn.ParseEndpoint(raw, c.config.AllowLoopbackHTTP)
	if err != nil {
		return false
	}
	if c.config.EndpointValidator != nil {
		candidate := *parsed
		return c.config.EndpointValidator(&candidate) == nil
	}
	return true
}

func validDeviceAuthorization(value DeviceAuthorization, now time.Time) bool {
	return value.deviceCode != "" && len(value.deviceCode) <= maxDeviceValueBytes && value.ExpiresIn > 0 && value.ExpiresIn <= maxDeviceLifetime &&
		value.Interval > 0 && value.Interval <= maxDeviceInterval && !value.ExpiresAt.IsZero() && now.Before(value.ExpiresAt) &&
		value.ExpiresAt.Sub(now) <= time.Duration(maxDeviceLifetime)*time.Second
}

func parseDeviceError(body []byte, status int) error {
	if err := authn.ValidateJSON(body, authn.JSONOptions{MaxBytes: defaultMaxResponseBytes, MaxDepth: 4, MaxMembers: 16}); err != nil {
		return &DeviceError{StatusCode: status}
	}
	var value struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(body, &value) != nil || value.Error == "" || len(value.Error) > 256 {
		return &DeviceError{StatusCode: status}
	}
	return &DeviceError{Code: value.Error, StatusCode: status}
}

func waitContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
