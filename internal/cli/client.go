package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/example/ocservapi/internal/audit"
	"github.com/example/ocservapi/internal/auth"
	"github.com/example/ocservapi/internal/store"
	"github.com/example/ocservapi/internal/system"
)

type Client struct {
	baseURL    string
	httpClient *http.Client
	token      string
}

type Session struct {
	API   string `json:"api"`
	Token string `json:"token"`
}

func NewClient(baseURL, token string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		token: token,
	}
}

func (c *Client) Health(ctx context.Context) (map[string]any, error) {
	var resp map[string]any
	if err := c.do(ctx, http.MethodGet, "/health", nil, false, &resp); err != nil {
		return nil, err
	}
	return resp, nil
}

func (c *Client) Login(ctx context.Context, username string) (string, auth.User, error) {
	payload := map[string]string{"username": username}
	var resp struct {
		Token string    `json:"token"`
		User  auth.User `json:"user"`
	}
	if err := c.do(ctx, http.MethodPost, "/api/v1/auth/login", payload, false, &resp); err != nil {
		return "", auth.User{}, err
	}
	return resp.Token, resp.User, nil
}

func (c *Client) WhoAmI(ctx context.Context) (auth.User, error) {
	var resp auth.User
	if err := c.do(ctx, http.MethodGet, "/api/v1/auth/whoami", nil, true, &resp); err != nil {
		return auth.User{}, err
	}
	return resp, nil
}

func (c *Client) SystemInfo(ctx context.Context) (system.Info, error) {
	var resp system.Info
	if err := c.do(ctx, http.MethodGet, "/api/v1/system/info", nil, true, &resp); err != nil {
		return system.Info{}, err
	}
	return resp, nil
}

func (c *Client) Endpoints(ctx context.Context) ([]store.Endpoint, error) {
	var resp struct {
		Items []store.Endpoint `json:"items"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/v1/endpoints", nil, true, &resp); err != nil {
		return nil, err
	}
	return resp.Items, nil
}

func (c *Client) Deployments(ctx context.Context) ([]store.Deployment, error) {
	var resp struct {
		Items []store.Deployment `json:"items"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/v1/deployments", nil, true, &resp); err != nil {
		return nil, err
	}
	return resp.Items, nil
}

func (c *Client) Audit(ctx context.Context) ([]audit.Event, error) {
	var resp struct {
		Items []audit.Event `json:"items"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/v1/audit", nil, true, &resp); err != nil {
		return nil, err
	}
	return resp.Items, nil
}

func (c *Client) Access(ctx context.Context) ([]store.AccessSummary, error) {
	var resp struct {
		Items []store.AccessSummary `json:"items"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/v1/access", nil, true, &resp); err != nil {
		return nil, err
	}
	return resp.Items, nil
}

func (c *Client) do(ctx context.Context, method, path string, payload any, authRequired bool, out any) error {
	var bodyReader *bytes.Reader
	if payload == nil {
		bodyReader = bytes.NewReader(nil)
	} else {
		data, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bodyReader)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if authRequired {
		if c.token == "" {
			return fmt.Errorf("not logged in")
		}
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("perform request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		var apiErr map[string]string
		_ = json.NewDecoder(resp.Body).Decode(&apiErr)
		if message := apiErr["error"]; message != "" {
			return fmt.Errorf(message)
		}
		return fmt.Errorf("unexpected status %s", resp.Status)
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

func DefaultSessionPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".occtl-session.json"
	}
	return filepath.Join(home, ".config", "occtl", "session.json")
}

func SaveSession(path string, session Session) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create session dir: %w", err)
	}
	data, err := json.MarshalIndent(session, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal session: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write session: %w", err)
	}
	return nil
}

func LoadSession(path string) (Session, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Session{}, err
	}
	var session Session
	if err := json.Unmarshal(data, &session); err != nil {
		return Session{}, fmt.Errorf("parse session: %w", err)
	}
	return session, nil
}
