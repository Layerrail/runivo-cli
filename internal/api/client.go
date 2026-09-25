// Package api communicates only with the Runivo control plane. It has no engine credentials.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const DefaultURL = "https://runivo-dashboard.vercel.app"
const MaxResponse = 16 << 20

type Error struct {
	Status     int
	Code       string
	Message    string
	RetryAfter string
}

func (e *Error) Error() string { return e.Message }

type Client struct {
	BaseURL string
	Token   string
	Version string
	HTTP    *http.Client
}

func ValidateURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return "", errors.New("API URL must be an HTTPS origin without a path, credentials or query")
	}
	local := u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost" || u.Hostname() == "::1"
	if u.Scheme != "https" && !(u.Scheme == "http" && local) {
		return "", errors.New("API URL must use HTTPS (HTTP is allowed only on localhost)")
	}
	return strings.TrimRight(u.String(), "/"), nil
}
func New(base, token, version string) (*Client, error) {
	canonical, err := ValidateURL(base)
	if err != nil {
		return nil, err
	}
	return &Client{BaseURL: canonical, Token: token, Version: version, HTTP: &http.Client{Timeout: 45 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func (c *Client) Request(ctx context.Context, method, path string, body any, idempotency string) (*http.Response, error) {
	if !strings.HasPrefix(path, "/api/v1/") || strings.ContainsAny(path, "\r\n") {
		return nil, errors.New("API paths must start with /api/v1/")
	}
	parsed, err := url.Parse(path)
	if err != nil || parsed.IsAbs() || parsed.Host != "" {
		return nil, errors.New("invalid API path")
	}
	var data []byte
	if body != nil {
		data, err = json.Marshal(body)
		if err != nil {
			return nil, err
		}
		if len(data) > 262144 {
			return nil, errors.New("request exceeds the API's 256 KiB limit")
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "runivo-cli/"+c.Version)
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	if idempotency != "" {
		req.Header.Set("Idempotency-Key", idempotency)
	}
	response, err := c.HTTP.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("could not reach Runivo; check your connection and API URL")
	}
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		return response, nil
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(response.Body, 65536))
	var payload struct {
		Error  string `json:"error"`
		Errors []struct {
			Message string `json:"message"`
			Code    string `json:"code"`
		} `json:"errors"`
	}
	_ = json.Unmarshal(raw, &payload)
	e := &Error{Status: response.StatusCode, Code: payload.Error, RetryAfter: response.Header.Get("Retry-After"), Message: fmt.Sprintf("Runivo returned HTTP %d", response.StatusCode)}
	if payload.Error != "" {
		e.Message = payload.Error
	} else if len(payload.Errors) > 0 {
		e.Message = payload.Errors[0].Message
		e.Code = payload.Errors[0].Code
	}
	if response.StatusCode >= 300 && response.StatusCode < 400 {
		e.Message = "Runivo returned a redirect; credentials were not forwarded. Check your API URL"
	}
	return nil, e
}
func (c *Client) Do(ctx context.Context, method, path string, body any, out any, idempotency string) error {
	response, err := c.Request(ctx, method, path, body, idempotency)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, MaxResponse+1))
	if err != nil {
		return errors.New("incomplete API response")
	}
	if len(raw) > MaxResponse {
		return errors.New("API response exceeded 16 MiB")
	}
	if out == nil || len(raw) == 0 {
		return nil
	}
	if err = json.Unmarshal(raw, out); err != nil {
		return errors.New("Runivo returned an unexpected response format")
	}
	return nil
}
