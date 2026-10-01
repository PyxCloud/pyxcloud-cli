package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/pyxcloud/pyxcloud-cli/internal/config"
)

// OAuth2 Device Authorization Grant (RFC 8628) login, used for machines
// without a local browser callback (containers, remote shells). The user
// approves the login on any device at the IdP's verification URL.

const deviceGrantType = "urn:ietf:params:oauth:grant-type:device_code"

// devicePollFallback is the polling interval in seconds when the IdP omits
// one (RFC 8628 §3.5); tests shorten it to keep the suite fast.
var devicePollFallback = 5

// deviceAuthResponse is the IdP's device authorization response (RFC 8628 §3.2).
type deviceAuthResponse struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
}

// deviceLogin runs the device authorization grant against authURL (Keycloak
// realm URL) and stores the resulting tokens in the backendURL profile config.
func deviceLogin(authURL, clientID, backendURL string) error {
	if authURL == "" {
		authURL = "https://beta-auth.pyxcloud.io/realms/pyx"
	}
	if clientID == "" {
		clientID = "pyxcloud-cli"
	}

	deviceResp, err := requestDeviceCode(authURL, clientID)
	if err != nil {
		return fmt.Errorf("device authorization: %w", err)
	}

	interval := deviceResp.Interval
	if interval <= 0 {
		interval = devicePollFallback
	}

	complete := deviceResp.VerificationURIComplete
	if complete == "" {
		complete = deviceResp.VerificationURI
	}
	fmt.Println("Open this URL on any device to authenticate:")
	fmt.Printf("  %s\n", complete)
	fmt.Printf("Enter code: %s\n", deviceResp.UserCode)
	if err := openBrowser(complete); err != nil {
		logv("could not open browser automatically: %v", err)
	}

	accessToken, refreshToken, err := pollDeviceToken(authURL, clientID, deviceResp.DeviceCode, interval)
	if err != nil {
		return err
	}

	cfg := &config.Config{
		Token:        accessToken,
		RefreshToken: refreshToken,
		APIURL:       backendURL,
		AuthURL:      authURL,
		ClientID:     clientID,
	}
	if err := config.SaveProfile(profile, cfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	fmt.Println("Authenticated via device flow.")
	fmt.Printf("  API: %s\n", backendURL)
	if profile != "" {
		fmt.Printf("  Profile: %s\n", profile)
	}
	return nil
}

// requestDeviceCode starts the device authorization grant.
func requestDeviceCode(authURL, clientID string) (*deviceAuthResponse, error) {
	endpoint := authURL + "/protocol/openid-connect/auth/device"
	resp, err := http.PostForm(endpoint, url.Values{"client_id": {clientID}})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("device endpoint returned HTTP %d: %s", resp.StatusCode, string(body))
	}
	var out deviceAuthResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("decode device response: %w", err)
	}
	if out.DeviceCode == "" || out.VerificationURI == "" {
		return nil, fmt.Errorf("device endpoint response missing device_code/verification_uri")
	}
	return &out, nil
}

// pollDeviceToken polls the token endpoint until the user approves, the
// request is denied or the device code expires.
func pollDeviceToken(authURL, clientID, deviceCode string, interval int) (string, string, error) {
	tokenEndpoint := authURL + "/protocol/openid-connect/token"
	deadline := time.Now().Add(10 * time.Minute)
	for {
		time.Sleep(time.Duration(interval) * time.Second)
		if time.Now().After(deadline) {
			return "", "", fmt.Errorf("device login timed out")
		}

		data := url.Values{}
		data.Set("grant_type", deviceGrantType)
		data.Set("client_id", clientID)
		data.Set("device_code", deviceCode)

		resp, err := http.PostForm(tokenEndpoint, data)
		if err != nil {
			return "", "", err
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		var result map[string]interface{}
		_ = json.Unmarshal(body, &result)

		if resp.StatusCode == 200 {
			accessToken, _ := result["access_token"].(string)
			refreshToken, _ := result["refresh_token"].(string)
			if accessToken == "" {
				return "", "", fmt.Errorf("no access_token in token response")
			}
			return accessToken, refreshToken, nil
		}

		errCode, _ := result["error"].(string)
		switch errCode {
		case "authorization_pending":
			// keep polling
		case "slow_down":
			interval += 5
		default:
			return "", "", fmt.Errorf("device token endpoint returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
		}
	}
}
