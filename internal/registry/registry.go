// Package registry reads what an image declares from its registry, without pulling it: the ports it
// exposes, for the app form to fill in.
package registry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Ref is an image reference split into where it lives and which version.
type Ref struct {
	Host      string // registry host, registry-1.docker.io for Docker Hub
	Repo      string // library/nginx
	Reference string // tag or digest
}

// ParseRef splits an image name the way Docker does: no host means Docker Hub, no tag means latest.
func ParseRef(image string) (Ref, error) {
	image = strings.TrimSpace(image)
	if image == "" || strings.ContainsAny(image, " \t\n") {
		return Ref{}, errors.New("image invalide")
	}
	r := Ref{Host: "registry-1.docker.io", Reference: "latest"}
	name := image
	if i := strings.IndexByte(name, '@'); i >= 0 {
		name, r.Reference = name[:i], name[i+1:]
	} else if i := strings.LastIndexByte(name, ':'); i > strings.LastIndexByte(name, '/') {
		name, r.Reference = name[:i], name[i+1:]
	}
	parts := strings.Split(name, "/")
	if len(parts) > 1 && (strings.ContainsAny(parts[0], ".:") || parts[0] == "localhost") {
		r.Host = parts[0]
		parts = parts[1:]
		if r.Host == "docker.io" || r.Host == "index.docker.io" {
			r.Host = "registry-1.docker.io"
		}
	}
	if r.Host == "registry-1.docker.io" && len(parts) == 1 {
		parts = []string{"library", parts[0]}
	}
	r.Repo = strings.ToLower(strings.Join(parts, "/"))
	if r.Repo == "" || r.Reference == "" {
		return Ref{}, errors.New("image invalide")
	}
	return r, nil
}

// PublicClient only reaches public addresses: image names come from users, and must not make the server
// call machines of its own network.
func PublicClient() *http.Client {
	dialer := &net.Dialer{
		Timeout: 5 * time.Second,
		Control: func(network, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			ip := net.ParseIP(host)
			if ip == nil || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() || ip.IsMulticast() {
				return fmt.Errorf("adresse non publique refusée : %s", host)
			}
			return nil
		},
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.DialContext = dialer.DialContext
	tr.Proxy = nil
	return &http.Client{Timeout: 15 * time.Second, Transport: tr}
}

const manifestTypes = "application/vnd.oci.image.index.v1+json, application/vnd.docker.distribution.manifest.list.v2+json, " +
	"application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.v2+json"

// ExposedPorts returns the TCP ports an image exposes (its EXPOSE lines), for linux/amd64 when it has
// several platforms. Only public images are read: the server has no registry credentials.
func ExposedPorts(ctx context.Context, client *http.Client, image string) ([]int, error) {
	ref, err := ParseRef(image)
	if err != nil {
		return nil, err
	}
	c := &session{client: client, ref: ref}
	var m struct {
		Manifests []struct {
			Digest   string `json:"digest"`
			Platform struct {
				OS           string `json:"os"`
				Architecture string `json:"architecture"`
			} `json:"platform"`
		} `json:"manifests"`
		Config struct {
			Digest string `json:"digest"`
		} `json:"config"`
	}
	if err := c.get(ctx, "/manifests/"+ref.Reference, manifestTypes, &m); err != nil {
		return nil, err
	}
	if len(m.Manifests) > 0 {
		digest := ""
		for _, p := range m.Manifests {
			if p.Platform.OS != "linux" {
				continue
			}
			if digest == "" || p.Platform.Architecture == "amd64" {
				digest = p.Digest
			}
		}
		if digest == "" {
			return nil, errors.New("aucune version linux de cette image")
		}
		if err := c.get(ctx, "/manifests/"+digest, manifestTypes, &m); err != nil {
			return nil, err
		}
	}
	if m.Config.Digest == "" {
		return nil, errors.New("manifeste sans configuration")
	}
	var cfg struct {
		Config struct {
			ExposedPorts map[string]struct{} `json:"ExposedPorts"`
		} `json:"config"`
	}
	if err := c.get(ctx, "/blobs/"+m.Config.Digest, "", &cfg); err != nil {
		return nil, err
	}
	ports := []int{}
	for p := range cfg.Config.ExposedPorts {
		num, proto, _ := strings.Cut(p, "/")
		if n, err := strconv.Atoi(num); err == nil && n > 0 && n < 65536 && (proto == "" || proto == "tcp") {
			ports = append(ports, n)
		}
	}
	slices.Sort(ports)
	return slices.Compact(ports), nil
}

// session talks to one repository, with the anonymous token its registry asks for.
type session struct {
	client *http.Client
	ref    Ref
	token  string
}

func (c *session) get(ctx context.Context, path, accept string, out any) error {
	u := "https://" + c.ref.Host + "/v2/" + c.ref.Repo + path
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return err
		}
		if accept != "" {
			req.Header.Set("Accept", accept)
		}
		if c.token != "" {
			req.Header.Set("Authorization", "Bearer "+c.token)
		}
		resp, err := c.client.Do(req)
		if err != nil {
			return err
		}
		if resp.StatusCode == http.StatusUnauthorized && attempt == 0 {
			challenge := resp.Header.Get("WWW-Authenticate")
			resp.Body.Close()
			if err := c.authenticate(ctx, challenge); err != nil {
				return err
			}
			continue
		}
		defer resp.Body.Close()
		switch {
		case resp.StatusCode == http.StatusNotFound:
			return errors.New("image introuvable sur son registre")
		case resp.StatusCode == http.StatusTooManyRequests:
			return errors.New("le registre limite les lectures anonymes (Docker Hub : quelques-unes par heure et par IP) : réessaie plus tard")
		case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
			return errors.New("image privée : le registre refuse de la décrire")
		case resp.StatusCode != http.StatusOK:
			return fmt.Errorf("registre : %s", resp.Status)
		}
		return json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(out)
	}
}

// authenticate gets an anonymous pull token from the realm a registry names in its 401 answer.
func (c *session) authenticate(ctx context.Context, challenge string) error {
	scheme, params, _ := strings.Cut(challenge, " ")
	if !strings.EqualFold(scheme, "Bearer") {
		return errors.New("image privée : le registre refuse de la décrire")
	}
	p := map[string]string{}
	for _, kv := range splitParams(params) {
		k, v, _ := strings.Cut(kv, "=")
		p[strings.ToLower(strings.TrimSpace(k))] = strings.Trim(strings.TrimSpace(v), `"`)
	}
	realm, err := url.Parse(p["realm"])
	if err != nil || realm.Scheme != "https" {
		return errors.New("registre : authentification inattendue")
	}
	q := realm.Query()
	if p["service"] != "" {
		q.Set("service", p["service"])
	}
	scope := p["scope"]
	if scope == "" {
		scope = "repository:" + c.ref.Repo + ":pull"
	}
	q.Set("scope", scope)
	realm.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, realm.String(), nil)
	if err != nil {
		return err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return errors.New("image privée : le registre refuse de la décrire")
	}
	var t struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&t); err != nil {
		return err
	}
	c.token = t.Token
	if c.token == "" {
		c.token = t.AccessToken
	}
	return nil
}

// splitParams splits a challenge's comma-separated parameters, ignoring commas inside quotes.
func splitParams(s string) []string {
	var out []string
	quoted, start := false, 0
	for i, ch := range s {
		switch {
		case ch == '"':
			quoted = !quoted
		case ch == ',' && !quoted:
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}
