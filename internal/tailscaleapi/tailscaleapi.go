// Package tailscaleapi is a minimal client for the one thing EdgeGrid needs
// from Tailscale's management API: minting and revoking auth keys, so a
// coordinator operator can hand a new device tailnet access without
// inviting anyone as a tailnet member and without the joining person ever
// seeing a Tailscale login screen (tsnet authenticates directly against the
// key — see agent.NewAgent's tsnet.Server.AuthKey).
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

	"github.com/edgegrid/edgegrid/internal/nodeident"
)

const apiBase = "https://api.tailscale.com/api/v2"

// Client mints/revokes Tailscale auth keys using OAuth client credentials
// scoped to auth_keys. Only ever loaded for a primary coordinator — see
// LoadCredentials.
type Client struct {
	tailnet      string
	clientID     string
	clientSecret string
	tag          string
	httpClient   *http.Client

	token       string
	tokenExpiry time.Time
}

// LoadCredentials reads the OAuth client id/secret + tailnet name from
// dataDir, written once by the operator into ts_api_client_id,
// ts_api_client_secret, and ts_api_tailnet — same 0600-file convention as
// every other credential in this directory (see nodeident.SaveToken).
// Returns nil if not configured; minting is opt-in, not required to run a
// coordinator.
//
// ts_api_tag is loaded too, but not required to construct a Client — a
// missing tag only blocks CreateKey (Tailscale requires a tag-scoped OAuth
// client's keys to carry that tag), not RevokeKey, which needs no tag.
func LoadCredentials(dataDir string) *Client {
	id := nodeident.LoadToken(dataDir, "ts_api_client_id")
	secret := nodeident.LoadToken(dataDir, "ts_api_client_secret")
	tailnet := nodeident.LoadToken(dataDir, "ts_api_tailnet")
	if id == "" || secret == "" || tailnet == "" {
		return nil
	}
	return &Client{
		tailnet:      tailnet,
		clientID:     id,
		clientSecret: secret,
		tag:          nodeident.LoadToken(dataDir, "ts_api_tag"),
		httpClient:   &http.Client{Timeout: 15 * time.Second},
	}
}

// accessToken exchanges the OAuth client credentials for a short-lived
// bearer token, reusing it until shortly before it expires.
func (c *Client) accessToken() (string, error) {
	if c.token != "" && time.Now().Before(c.tokenExpiry) {
		return c.token, nil
	}
	// Standard OAuth2 client_credentials grant (RFC 6749) — verified against
	// tailscale/tailscale-client-go's own WithOAuthClientCredentials, which
	// hits this same relative path via golang.org/x/oauth2/clientcredentials.
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

// MintedKey is returned once, right after minting — Key is never available
// again after this; nothing in EdgeGrid persists it.
type MintedKey struct {
	ID  string
	Key string
}

// CreateKey mints a single-use, pre-authorized auth key: a joining node
// consumes it once (tsnet.Server.AuthKey), and it can't be reused
// afterward, so one mint always maps to at most one device.
func (c *Client) CreateKey() (*MintedKey, error) {
	if c.tag == "" {
		return nil, fmt.Errorf("no tag configured (set Tailscale API Tag in Settings) — " +
			"this OAuth client is tag-scoped, so Tailscale requires every key it mints to carry that tag")
	}
	token, err := c.accessToken()
	if err != nil {
		return nil, err
	}
	// Field names/nesting verified against tailscale/tailscale-client-go's
	// KeyCapabilities struct (devices.create.{reusable,ephemeral,tags,preauthorized}),
	// the official Go client Tailscale itself maintains for this API.
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

// RevokeKey deletes a key from the tailnet so it can no longer be used to
// join. Revoking doesn't remove a device that already joined with it.
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
