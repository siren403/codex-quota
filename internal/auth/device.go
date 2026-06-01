package auth

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/siren403/codex-quota/internal/config"
)

const (
	deviceUserCodeURL  = "https://auth.openai.com/api/accounts/deviceauth/usercode"
	deviceTokenPollURL = "https://auth.openai.com/api/accounts/deviceauth/token"
	DeviceVerifyURL    = "https://auth.openai.com/codex/device"
)

type DeviceLoginStatus struct {
	UserCode  string
	VerifyURL string
}

type deviceUserCodeRequest struct {
	ClientID string `json:"client_id"`
}

// flexInt unmarshals JSON values that may be either a number or a quoted string.
type flexInt int

func (f *flexInt) UnmarshalJSON(data []byte) error {
	s := strings.Trim(string(data), `"`)
	n, err := strconv.Atoi(s)
	if err != nil {
		return err
	}
	*f = flexInt(n)
	return nil
}

type deviceUserCodeResponse struct {
	DeviceAuthID string  `json:"device_auth_id"`
	UserCode     string  `json:"user_code"`
	Interval     flexInt `json:"interval"`
}

type deviceTokenRequest struct {
	DeviceAuthID string `json:"device_auth_id"`
	UserCode     string `json:"user_code"`
}

type deviceTokenPollResponse struct {
	AuthorizationCode string `json:"authorization_code"`
	CodeVerifier      string `json:"code_verifier"`
}

type deviceLoginSession struct {
	userCode     string
	verifyURL    string
	deviceAuthID string
	interval     int
	done         chan struct{}
	result       *config.Account
	err          error
	finishOnce   sync.Once
}

var (
	deviceLoginMu     sync.Mutex
	activeDeviceLogin *deviceLoginSession
)

func StartDeviceLogin() (DeviceLoginStatus, error) {
	deviceLoginMu.Lock()
	defer deviceLoginMu.Unlock()

	if activeDeviceLogin != nil && !activeDeviceLogin.isDone() {
		return DeviceLoginStatus{
			UserCode:  activeDeviceLogin.userCode,
			VerifyURL: activeDeviceLogin.verifyURL,
		}, nil
	}

	resp, err := requestDeviceUserCode()
	if err != nil {
		return DeviceLoginStatus{}, err
	}

	interval := int(resp.Interval)
	if interval <= 0 {
		interval = 5
	}

	session := &deviceLoginSession{
		userCode:     resp.UserCode,
		verifyURL:    DeviceVerifyURL,
		deviceAuthID: resp.DeviceAuthID,
		interval:     interval,
		done:         make(chan struct{}),
	}
	activeDeviceLogin = session

	go session.poll()

	return DeviceLoginStatus{
		UserCode:  session.userCode,
		VerifyURL: session.verifyURL,
	}, nil
}

func PollDeviceLogin() (*config.Account, bool, error) {
	deviceLoginMu.Lock()
	session := activeDeviceLogin
	if session == nil {
		deviceLoginMu.Unlock()
		return nil, true, ErrLoginCancelled
	}
	if !session.isDone() {
		deviceLoginMu.Unlock()
		return nil, false, nil
	}
	activeDeviceLogin = nil
	deviceLoginMu.Unlock()
	return session.result, true, session.err
}

func CancelDeviceLogin() {
	deviceLoginMu.Lock()
	session := activeDeviceLogin
	activeDeviceLogin = nil
	deviceLoginMu.Unlock()
	if session != nil {
		session.finish(nil, ErrLoginCancelled)
	}
}

func (s *deviceLoginSession) isDone() bool {
	select {
	case <-s.done:
		return true
	default:
		return false
	}
}

func (s *deviceLoginSession) finish(account *config.Account, err error) {
	s.finishOnce.Do(func() {
		s.result = account
		s.err = err
		close(s.done)
	})
}

func (s *deviceLoginSession) poll() {
	timeout := time.After(15 * time.Minute)
	interval := time.Duration(s.interval) * time.Second

	for {
		select {
		case <-s.done:
			return
		case <-timeout:
			s.finish(nil, fmt.Errorf("device auth timed out after 15 minutes"))
			return
		case <-time.After(interval):
		}

		pollResp, pending, err := pollDeviceToken(s.deviceAuthID, s.userCode)
		if err != nil {
			s.finish(nil, err)
			return
		}
		if pending {
			continue
		}

		tokenResp, err := exchangeDeviceCode(pollResp)
		if err != nil {
			s.finish(nil, err)
			return
		}

		account, err := accountFromTokenResponse(tokenResp)
		s.finish(account, err)
		return
	}
}

func requestDeviceUserCode() (*deviceUserCodeResponse, error) {
	body, err := json.Marshal(deviceUserCodeRequest{ClientID: oauthClientID})
	if err != nil {
		return nil, fmt.Errorf("failed to build device code request: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, deviceUserCodeURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create device code request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("device code request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("device code request returned %d: %s", resp.StatusCode, strings.TrimSpace(string(bodyBytes)))
	}

	var result deviceUserCodeResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode device code response: %w", err)
	}
	if result.DeviceAuthID == "" || result.UserCode == "" {
		return nil, fmt.Errorf("invalid device code response: missing fields")
	}
	return &result, nil
}

func pollDeviceToken(deviceAuthID, userCode string) (*deviceTokenPollResponse, bool, error) {
	body, err := json.Marshal(deviceTokenRequest{
		DeviceAuthID: deviceAuthID,
		UserCode:     userCode,
	})
	if err != nil {
		return nil, false, fmt.Errorf("failed to build poll request: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, deviceTokenPollURL, bytes.NewReader(body))
	if err != nil {
		return nil, false, fmt.Errorf("failed to create poll request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, false, fmt.Errorf("poll request failed: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, _ := io.ReadAll(resp.Body)
	bodyText := strings.TrimSpace(string(bodyBytes))

	if resp.StatusCode != http.StatusOK {
		lower := strings.ToLower(bodyText)
		if strings.Contains(lower, "pending") || strings.Contains(lower, "waiting") ||
			resp.StatusCode == http.StatusAccepted || resp.StatusCode == http.StatusUnauthorized {
			return nil, true, nil
		}
		if len(bodyText) > 300 {
			bodyText = bodyText[:300]
		}
		return nil, false, fmt.Errorf("poll returned %d: %s", resp.StatusCode, bodyText)
	}

	var result deviceTokenPollResponse
	if err := json.Unmarshal(bodyBytes, &result); err != nil {
		return nil, false, fmt.Errorf("failed to decode poll response: %w", err)
	}
	if result.AuthorizationCode == "" {
		return nil, true, nil
	}
	return &result, false, nil
}

func exchangeDeviceCode(pollResp *deviceTokenPollResponse) (*tokenExchangeResponse, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("client_id", oauthClientID)
	form.Set("code", pollResp.AuthorizationCode)
	form.Set("code_verifier", pollResp.CodeVerifier)
	form.Set("redirect_uri", redirectURI)

	req, err := http.NewRequest(http.MethodPost, oauthTokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("failed to create token exchange request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token exchange request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		bodyText := strings.TrimSpace(string(bodyBytes))
		if len(bodyText) > 500 {
			bodyText = bodyText[:500]
		}
		return nil, fmt.Errorf("token exchange failed with status %d: %s", resp.StatusCode, bodyText)
	}

	var tokenResp tokenExchangeResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return nil, fmt.Errorf("failed to decode token response: %w", err)
	}
	if tokenResp.AccessToken == "" || tokenResp.RefreshToken == "" {
		return nil, fmt.Errorf("token response missing fields")
	}

	return &tokenResp, nil
}
