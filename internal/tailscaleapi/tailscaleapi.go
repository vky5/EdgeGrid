// Package tailscaleapi makes and revokes Tailscale join keys through Tailscale's API.
// so a coordinator operator can let a new device join without inviting anyone or showing a Tailscale login
package tailscaleapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/edgegrid/edgegrid/internal/node"
)

const apiBase = "https://api.tailscale.com/api/v2"

// makes and revokes join keys with this profile's Tailscale API login
// only ever loaded for a primary coordinator
type Client struct {
	tailnet      string
	clientID     string
	clientSecret string
	tag          string
	httpClient   *http.Client

	token       string
	tokenExpiry time.Time
}

// reads the Tailscale API login from dataDir, e.g. ts_api_client_id, or returns nil if it isn't set up
// ts_api_tag is optional here: only making keys needs it, since Tailscale wants the tag on every key; revoking doesn't
func LoadCredentials(dataDir string) *Client {
	id := node.LoadToken(dataDir, "ts_api_client_id")
	secret := node.LoadToken(dataDir, "ts_api_client_secret")
	tailnet := node.LoadToken(dataDir, "ts_api_tailnet")
	if id == "" || secret == "" || tailnet == "" {
		return nil
	}
	return &Client{
		tailnet:      tailnet,
		clientID:     id,
		clientSecret: secret,
		tag:          node.LoadToken(dataDir, "ts_api_tag"),
		httpClient:   &http.Client{Timeout: 15 * time.Second},
	}
}

// swaps the API login for a short-lived token, reusing it until just before it expires
func (c *Client) accessToken() (string, error) {
	if c.token != "" && time.Now().Before(c.tokenExpiry) {
		return c.token, nil
	}
	// same request as tailscale-client-go's WithOAuthClientCredentials (verified)
	form := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {c.clientID},
		"client_secret": {c.clientSecret},
	}
	req, err := http.NewRequest(http.MethodPost, "https://api.tailscale.com/api/v2/oauth/token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("tailscale oauth token: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("tailscale oauth token: %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	var body struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", err
	}
	c.token = body.AccessToken
	c.tokenExpiry = time.Now().Add(time.Duration(body.ExpiresIn-30) * time.Second)
	return c.token, nil
}

// a new join key, shown once; EdgeGrid never saves Key, so it can't be shown again
type MintedKey struct {
	ID  string
	Key string
}

// makes a one-use join key that needs no approval, so one key lets in at most one device
func (c *Client) CreateKey() (*MintedKey, error) {
	if c.tag == "" {
		return nil, fmt.Errorf("no tag configured (set Tailscale API Tag in Settings) — " +
			"this OAuth client is tag-scoped, so Tailscale requires every key it mints to carry that tag")
	}
	token, err := c.accessToken()
	if err != nil {
		return nil, err
	}
	// field names verified against tailscale-client-go's KeyCapabilities
	reqBody, _ := json.Marshal(map[string]any{
		"capabilities": map[string]any{
			"devices": map[string]any{
				"create": map[string]any{
					"reusable":      false,
					"ephemeral":     false,
					"tags":          []string{c.tag},
					"preauthorized": true,
				},
			},
		},
		"description": "edgegrid",
	})
	reqURL := fmt.Sprintf("%s/tailnet/%s/keys", apiBase, c.tailnet)
	req, err := http.NewRequest(http.MethodPost, reqURL, bytes.NewReader(reqBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("tailscale create key: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("tailscale create key: %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	var body struct {
		ID  string `json:"id"`
		Key string `json:"key"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	return &MintedKey{ID: body.ID, Key: body.Key}, nil
}

// deletes a join key so it can't be used again; a device that already joined stays
func (c *Client) RevokeKey(id string) error {
	token, err := c.accessToken()
	if err != nil {
		return err
	}
	reqURL := fmt.Sprintf("%s/tailnet/%s/keys/%s", apiBase, c.tailnet, id)
	req, err := http.NewRequest(http.MethodDelete, reqURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("tailscale revoke key: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("tailscale revoke key: %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	return nil
}
