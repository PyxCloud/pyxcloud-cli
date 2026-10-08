// Package passotransport provides a small, credential-safe HTTP client for the
// CLI's Passo API.
package passotransport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/pyxcloud/pyxcloud-cli/internal/passoauth"
)

const maxResponseBytes = 2 << 20

// Client sends authenticated requests to a Passo API base URL.
type Client struct {
	BaseURL     string
	HTTPClient  *http.Client
	AccessToken func(context.Context) (string, error)
}

// Response contains the raw response body. Export endpoints may return bytes
// that are not JSON; callers should handle those payloads without decoding.
type Response struct {
	StatusCode int
	Body       json.RawMessage
	RequestID  string
}

// APIError represents a non-success HTTP response. Its message intentionally
// excludes server-provided text and response bodies.
type APIError struct {
	StatusCode int
	Code       string
	RequestID  string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("API error %s (HTTP %d)", e.Code, e.StatusCode)
}

// New creates a client with a 30 second timeout and redirects disabled.
func New(baseURL string, token func(context.Context) (string, error)) *Client {
	return &Client{
		BaseURL: baseURL,
		HTTPClient: &http.Client{
			Timeout: 30 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return errors.New("redirects disabled")
			},
		},
		AccessToken: token,
	}
}

// Do sends a request. The path must be an origin-relative path beginning with
// one slash; the base URL's path prefix is retained.
func (c *Client) Do(ctx context.Context, method, path string, body json.RawMessage, idempotencyKey string) (Response, error) {
	return c.DoWithIfMatch(ctx, method, path, body, idempotencyKey, nil)
}

// DoWithIfMatch sends the explicitly supplied optimistic version as a decimal
// If-Match header. A nil version preserves Do's existing request behavior.
func (c *Client) DoWithIfMatch(ctx context.Context, method, path string, body json.RawMessage, idempotencyKey string, ifMatch *int64) (Response, error) {
	var zero Response
	if ifMatch != nil && *ifMatch < 0 {
		return zero, errors.New("invalid If-Match version")
	}
	parsedPath, err := url.Parse(path)
	if err != nil || !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") || parsedPath.IsAbs() || parsedPath.Host != "" || strings.Contains(path, "#") || strings.ContainsAny(path, "\r\n") {
		return zero, errors.New("invalid request path")
	}
	if len(body) > 0 && !json.Valid(body) {
		return zero, errors.New("invalid JSON request body")
	}
	base, err := url.Parse(c.BaseURL)
	if err != nil || base.Scheme == "" || base.Host == "" || base.User != nil || (base.Scheme != "http" && base.Scheme != "https") || base.RawQuery != "" || base.ForceQuery || strings.Contains(c.BaseURL, "#") {
		return zero, errors.New("invalid API base URL")
	}
	requestURL := *base
	basePath := strings.TrimSuffix(base.EscapedPath(), "/")
	requestURL.RawPath = basePath + parsedPath.EscapedPath()
	requestURL.Path, err = url.PathUnescape(requestURL.RawPath)
	if err != nil {
		return zero, errors.New("invalid request path")
	}
	requestURL.RawQuery = parsedPath.RawQuery
	requestURL.Fragment = ""

	var reader io.Reader
	if len(body) > 0 {
		reader = strings.NewReader(string(body))
	}
	req, err := http.NewRequestWithContext(ctx, method, requestURL.String(), reader)
	if err != nil {
		return zero, errors.New("request failed")
	}
	req.Header.Set("Accept", "application/json")
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	if idempotencyKey != "" && isMutating(method) {
		req.Header.Set("Idempotency-Key", idempotencyKey)
	}
	if ifMatch != nil {
		req.Header.Set("If-Match", strconv.FormatInt(*ifMatch, 10))
	}
	if c.AccessToken != nil {
		token, tokenErr := c.AccessToken(ctx)
		if tokenErr != nil {
			if ctx.Err() != nil {
				var phase *passoauth.PhaseError
				if errors.As(tokenErr, &phase) {
					return zero, phase
				}
				return zero, fmt.Errorf("authentication unavailable: %w", ctx.Err())
			}
			if errors.Is(tokenErr, passoauth.ErrCredentialAccessRequired) {
				return zero, passoauth.ErrCredentialAccessRequired
			}
			if errors.Is(tokenErr, passoauth.ErrCredentialStoreUnavailable) {
				return zero, passoauth.ErrCredentialStoreUnavailable
			}
			return zero, errors.New("authentication unavailable")
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
	}
	client := c.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	} else {
		cloned := *client
		client = &cloned
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return errors.New("redirects disabled") }
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return zero, passoauth.ContextPhase("http_request", ctx.Err())
		}
		return zero, errors.New("request failed")
	}
	defer resp.Body.Close()
	responseID := resp.Header.Get("X-Request-ID")
	if responseID == "" {
		responseID = resp.Header.Get("X-Request-Id")
	}
	responseID = sanitizeRequestID(responseID)
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		if ctx.Err() != nil {
			return zero, passoauth.ContextPhase("http_request", ctx.Err())
		}
		return zero, errors.New("request failed")
	}
	if len(data) > maxResponseBytes {
		return zero, errors.New("response exceeds 2 MiB limit")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return zero, &APIError{StatusCode: resp.StatusCode, Code: apiErrorCode(data), RequestID: responseID}
	}
	return Response{StatusCode: resp.StatusCode, Body: json.RawMessage(data), RequestID: responseID}, nil
}

func isMutating(method string) bool {
	switch strings.ToUpper(method) {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

func apiErrorCode(body []byte) string {
	var fields map[string]json.RawMessage
	if json.Unmarshal(body, &fields) == nil {
		for _, key := range []string{"error", "code", "errorCode"} {
			var code string
			if raw, ok := fields[key]; ok && json.Unmarshal(raw, &code) == nil && safeErrorCode(code) {
				return code
			}
		}
	}
	return "http_error"
}

func safeErrorCode(code string) bool {
	if len(code) == 0 || len(code) > 96 || !((code[0] >= 'A' && code[0] <= 'Z') || (code[0] >= 'a' && code[0] <= 'z')) {
		return false
	}
	for i := 1; i < len(code); i++ {
		c := code[i]
		if !((c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

func sanitizeRequestID(id string) string {
	if len(id) > 128 {
		return ""
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		if !((c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_' || c == '-' || c == '.' || c == ':') {
			return ""
		}
	}
	return id
}
