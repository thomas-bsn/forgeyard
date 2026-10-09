// Package cloudflare manages DNS records of a zone through the Cloudflare API, with a scoped API token.
package cloudflare

import (
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

// DefaultBaseURL is Cloudflare's API.
const DefaultBaseURL = "https://api.cloudflare.com/client/v4"

// Client calls the Cloudflare API with one token.
type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

// New returns a client for the production API.
func New(token string) *Client {
	return &Client{BaseURL: DefaultBaseURL, Token: token, HTTP: &http.Client{Timeout: 15 * time.Second}}
}

// Zone is a domain managed by Cloudflare.
type Zone struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type apiError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type envelope struct {
	Success bool            `json:"success"`
	Errors  []apiError      `json:"errors"`
	Result  json.RawMessage `json:"result"`
}

// ErrInvalidToken means Cloudflare rejected the token.
var ErrInvalidToken = errors.New("token Cloudflare invalide ou expiré")

// VerifyToken checks that the token is active.
func (c *Client) VerifyToken(ctx context.Context) error {
	var res struct {
		Status string `json:"status"`
	}
	if err := c.get(ctx, "/user/tokens/verify", &res); err != nil {
		return err
	}
	if res.Status != "active" {
		return ErrInvalidToken
	}
	return nil
}

// FindZone returns the zone holding domain: the domain itself or its closest parent (for
// "apps.example.com", the zone "example.com").
func (c *Client) FindZone(ctx context.Context, domain string) (Zone, error) {
	labels := strings.Split(strings.Trim(strings.ToLower(domain), "."), ".")
	for i := 0; i+1 < len(labels); i++ {
		name := strings.Join(labels[i:], ".")
		var zones []Zone
		if err := c.get(ctx, "/zones?name="+url.QueryEscape(name), &zones); err != nil {
			return Zone{}, err
		}
		if len(zones) > 0 {
			return zones[0], nil
		}
	}
	return Zone{}, fmt.Errorf("aucune zone Cloudflare accessible avec ce token ne contient %s : vérifiez que ses DNS sont gérés par Cloudflare et que le token a accès à la zone", domain)
}

func (c *Client) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("Cloudflare injoignable : %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return ErrInvalidToken
	}
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("réponse Cloudflare inattendue (%d)", resp.StatusCode)
	}
	if !env.Success {
		if len(env.Errors) > 0 {
			return fmt.Errorf("Cloudflare : %s", env.Errors[0].Message)
		}
		return fmt.Errorf("Cloudflare a répondu %d", resp.StatusCode)
	}
	return json.Unmarshal(env.Result, out)
}
