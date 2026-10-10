// Package discord implements the Discord OAuth2 authorization code flow used for "Sign in with Discord".
package discord

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Endpoints are the Discord URLs used by the flow. Tests point them at a fake server.
type Endpoints struct {
	Authorize string
	Token     string
	User      string
}

// DefaultEndpoints are Discord's production endpoints.
var DefaultEndpoints = Endpoints{
	Authorize: "https://discord.com/oauth2/authorize",
	Token:     "https://discord.com/api/oauth2/token",
	User:      "https://discord.com/api/users/@me",
}

// Config identifies a Discord application registered by the instance installer.
type Config struct {
	ClientID     string
	ClientSecret string
	RedirectURL  string
}

// User is the Discord profile returned for the identify and email scopes.
type User struct {
	ID         string `json:"id"`
	Username   string `json:"username"`
	GlobalName string `json:"global_name"`
	Avatar     string `json:"avatar"`
	Banner     string `json:"banner"`
	// AccentColor is the profile colour, as 0xRRGGBB; nil when the user has none.
	AccentColor *int64 `json:"accent_color"`
	Email       string `json:"email"`
	Verified    bool   `json:"verified"`
}

// DisplayName returns the name Discord shows for the user.
func (u User) DisplayName() string {
	if u.GlobalName != "" {
		return u.GlobalName
	}
	return u.Username
}

// Accent returns the profile colour, or -1 when there is none.
func (u User) Accent() int64 {
	if u.AccentColor == nil {
		return -1
	}
	return *u.AccentColor
}

// VerifiedEmail returns the email only when Discord has verified it.
func (u User) VerifiedEmail() string {
	if u.Verified {
		return u.Email
	}
	return ""
}

// Client talks to Discord.
type Client struct {
	HTTP      *http.Client
	Endpoints Endpoints
}

// NewClient returns a client for Discord's production endpoints.
func NewClient() *Client {
	return &Client{HTTP: &http.Client{Timeout: 10 * time.Second}, Endpoints: DefaultEndpoints}
}

// AuthorizeURL returns the Discord consent page URL the browser is sent to.
func (c *Client) AuthorizeURL(cfg Config, state string) string {
	q := url.Values{
		"client_id":     {cfg.ClientID},
		"redirect_uri":  {cfg.RedirectURL},
		"response_type": {"code"},
		"scope":         {"identify email"},
		"state":         {state},
		"prompt":        {"none"},
	}
	return c.Endpoints.Authorize + "?" + q.Encode()
}

// Authenticate exchanges an authorization code for an access token and returns the user's profile.
func (c *Client) Authenticate(ctx context.Context, cfg Config, code string) (User, error) {
	form := url.Values{
		"grant_type":   {"authorization_code"},
		"code":         {code},
		"redirect_uri": {cfg.RedirectURL},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Endpoints.Token, strings.NewReader(form.Encode()))
	if err != nil {
		return User{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(url.QueryEscape(cfg.ClientID), url.QueryEscape(cfg.ClientSecret))

	var token struct {
		AccessToken string `json:"access_token"`
	}
	if err := c.do(req, &token); err != nil {
		return User{}, fmt.Errorf("exchange code: %w", err)
	}

	req, err = http.NewRequestWithContext(ctx, http.MethodGet, c.Endpoints.User, nil)
	if err != nil {
		return User{}, err
	}
	req.Header.Set("Authorization", "Bearer "+token.AccessToken)
	var user User
	if err := c.do(req, &user); err != nil {
		return User{}, fmt.Errorf("fetch user: %w", err)
	}
	if user.ID == "" {
		return User{}, fmt.Errorf("fetch user: empty id")
	}
	return user, nil
}

func (c *Client) do(req *http.Request, out any) error {
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("discord returned %d: %s", resp.StatusCode, body)
	}
	return json.Unmarshal(body, out)
}
