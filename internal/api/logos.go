package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/thomas-bsn/forgeyard/internal/store/db"
)

// Logos of Docker images come from Docker Hub: the logo of official images, else the avatar of the
// image's publisher when they set one. Docker Hub has no documented API for logos, so the result is cached
// and the app's initial remains the fallback.

const (
	logoFoundTTL    = 30 * 24 * time.Hour
	logoNotFoundTTL = 7 * 24 * time.Hour
	maxLogoBytes    = 3 << 20 // as downloaded; stored logos are shrunk to logoSize
	logoSize        = 128
)

// hubRepo returns the Docker Hub repository of an image reference (nginx:1.27 → library/nginx), or "" for
// an image from another registry (ghcr.io/…).
func hubRepo(image string) string {
	ref := image
	if i := strings.IndexByte(ref, '@'); i >= 0 {
		ref = ref[:i]
	}
	if i := strings.LastIndexByte(ref, ':'); i > strings.LastIndexByte(ref, '/') {
		ref = ref[:i]
	}
	parts := strings.Split(strings.ToLower(ref), "/")
	if len(parts) > 1 && (strings.ContainsAny(parts[0], ".:") || parts[0] == "localhost") {
		if parts[0] != "docker.io" && parts[0] != "index.docker.io" && parts[0] != "registry-1.docker.io" {
			return ""
		}
		parts = parts[1:]
	}
	switch len(parts) {
	case 1:
		return "library/" + parts[0]
	case 2:
		return parts[0] + "/" + parts[1]
	}
	return ""
}

// imageLogoURL is where the UI loads the logo of an image, or "" when it cannot have one.
func imageLogoURL(image string) string {
	repo := hubRepo(image)
	if repo == "" {
		return ""
	}
	return "/api/logos?repo=" + url.QueryEscape(repo)
}

// handleImageLogo serves the cached logo of a Docker Hub repository, fetching it when unknown or stale.
func (s *Server) handleImageLogo(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	repo := r.URL.Query().Get("repo")
	if repo == "" || hubRepo(repo) != repo {
		http.NotFound(w, r)
		return
	}
	cached, err := s.store.GetImageLogo(ctx, repo)
	fresh := err == nil && time.Since(time.Unix(cached.FetchedAt, 0)) < map[bool]time.Duration{true: logoFoundTTL, false: logoNotFoundTTL}[cached.Found != 0]
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		s.internalError(w, r, err)
		return
	}
	if !fresh {
		fctx, cancel := context.WithTimeout(ctx, 8*time.Second)
		data, contentType := s.fetchHubLogo(fctx, repo)
		cancel()
		// A failure to reach Docker Hub keeps a logo found earlier.
		if data != nil || err != nil || cached.Found == 0 {
			cached = db.ImageLogo{Repo: repo, Found: boolInt(data != nil), ContentType: contentType, Data: data, FetchedAt: time.Now().Unix()}
			if err := s.store.PutImageLogo(ctx, db.PutImageLogoParams{
				Repo: repo, Found: cached.Found, ContentType: contentType, Data: data, FetchedAt: cached.FetchedAt,
			}); err != nil {
				s.logger.Warn("caching an image logo failed", "repo", repo, "err", err)
			}
		}
	}
	if cached.Found == 0 {
		w.Header().Set("Cache-Control", "private, max-age=86400")
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", cached.ContentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'")
	w.Header().Set("Cache-Control", "private, max-age=86400")
	w.Write(cached.Data)
}

// fetchHubLogo asks Docker Hub for a repository's logo, then for its publisher's avatar. It returns nil
// when there is none, or when Docker Hub answers something else than a small raster image.
func (s *Server) fetchHubLogo(ctx context.Context, repo string) ([]byte, string) {
	if data, ct := s.fetchImage(ctx, "https://hub.docker.com/api/media/repos_logo/v1/"+url.PathEscape(repo)+"?type=logo"); data != nil {
		return data, ct
	}
	namespace, _, _ := strings.Cut(repo, "/")
	if namespace == "library" {
		return nil, ""
	}
	// The publisher's avatar: organizations and users both expose a Gravatar URL. d=404 means "no default
	// picture", so a publisher without an avatar gives nothing instead of a silhouette.
	for _, kind := range []string{"orgs", "users"} {
		var profile struct {
			GravatarURL string `json:"gravatar_url"`
		}
		if !s.fetchJSON(ctx, "https://hub.docker.com/v2/"+kind+"/"+url.PathEscape(namespace)+"/", &profile) || profile.GravatarURL == "" {
			continue
		}
		u, err := url.Parse(profile.GravatarURL)
		if err != nil || u.Host != "www.gravatar.com" {
			continue
		}
		q := u.Query()
		q.Set("d", "404")
		q.Set("s", "128")
		u.RawQuery = q.Encode()
		if data, ct := s.fetchImage(ctx, u.String()); data != nil {
			return data, ct
		}
	}
	return nil, ""
}

func (s *Server) fetchImage(ctx context.Context, rawURL string) ([]byte, string) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, ""
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, ""
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxLogoBytes+1))
	if err != nil || len(data) == 0 || len(data) > maxLogoBytes {
		return nil, ""
	}
	ct := http.DetectContentType(data)
	if !avatarTypes[ct] {
		return nil, ""
	}
	return shrinkLogo(data, ct)
}

// shrinkLogo scales a large PNG, JPEG or GIF down to logoSize pixels on its longest side, averaging the
// pixels it merges, and encodes it as PNG. Small images, and WebP that the standard library cannot
// decode, are kept as they are.
func shrinkLogo(data []byte, contentType string) ([]byte, string) {
	if len(data) <= 64<<10 || contentType == "image/webp" {
		return data, contentType
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, ""
	}
	b := src.Bounds()
	scale := float64(logoSize) / float64(max(b.Dx(), b.Dy()))
	if scale >= 1 {
		return data, contentType
	}
	w, h := max(1, int(float64(b.Dx())*scale)), max(1, int(float64(b.Dy())*scale))
	dst := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		y0, y1 := b.Min.Y+y*b.Dy()/h, b.Min.Y+(y+1)*b.Dy()/h
		for x := 0; x < w; x++ {
			x0, x1 := b.Min.X+x*b.Dx()/w, b.Min.X+(x+1)*b.Dx()/w
			var r, g, bl, a, n uint64
			for sy := y0; sy < max(y1, y0+1); sy++ {
				for sx := x0; sx < max(x1, x0+1); sx++ {
					c := color.NRGBAModel.Convert(src.At(sx, sy)).(color.NRGBA)
					r, g, bl, a, n = r+uint64(c.R), g+uint64(c.G), bl+uint64(c.B), a+uint64(c.A), n+1
				}
			}
			dst.SetNRGBA(x, y, color.NRGBA{uint8(r / n), uint8(g / n), uint8(bl / n), uint8(a / n)})
		}
	}
	var out bytes.Buffer
	if err := png.Encode(&out, dst); err != nil {
		return nil, ""
	}
	return out.Bytes(), "image/png"
}

func (s *Server) fetchJSON(ctx context.Context, rawURL string, out any) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return false
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false
	}
	return jsonDecode(io.LimitReader(resp.Body, 1<<20), out) == nil
}

// appLogo describes how the UI draws an app's logo: an image when there is one (else, or when it fails to
// load, the initial on the colour).
type appLogo struct {
	Mode    string `json:"mode"`
	URL     string `json:"url,omitempty"`
	Color   string `json:"color,omitempty"`
	AutoURL string `json:"autoUrl,omitempty"` // the Docker Hub logo, to preview it while choosing
}

func toAppLogo(a db.App) appLogo {
	l := appLogo{Mode: a.LogoMode, Color: a.LogoColor, AutoURL: imageLogoURL(a.Image)}
	switch a.LogoMode {
	case "custom":
		l.URL = "/api/apps/" + strconv.FormatInt(a.ID, 10) + "/logo?v=" + strconv.FormatInt(a.LogoUpdatedAt, 10)
	case "auto":
		l.URL = imageLogoURL(a.Image)
	}
	return l
}

type logoRequest struct {
	Mode  string `json:"mode"`
	Color string `json:"color"`
	Image string `json:"image"` // a data URL, with mode "custom"
}

// handlePutAppLogo sets how an app's logo is chosen, with the uploaded picture for "custom".
func (s *Server) handlePutAppLogo(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	a, ok := s.appFromPath(w, r)
	if !ok {
		return
	}
	var body logoRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.Color != "" && !validColor(body.Color) {
		writeError(w, http.StatusBadRequest, "couleur invalide (#rrggbb)")
		return
	}
	var data []byte
	var contentType string
	switch body.Mode {
	case "auto", "initial":
	case "custom":
		if body.Image == "" {
			// Keep the picture already uploaded.
			if a.LogoUpdatedAt == 0 {
				writeError(w, http.StatusBadRequest, "envoyez une image")
				return
			}
			break
		}
		var err error
		if data, contentType, err = decodeDataURL(body.Image, maxAvatarBytes); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	default:
		writeError(w, http.StatusBadRequest, "mode inconnu : auto, custom ou initial")
		return
	}
	updated := a.LogoUpdatedAt
	if data != nil {
		updated = time.Now().UnixMilli()
	}
	if err := s.store.InTx(ctx, func(q *db.Queries) error {
		if data != nil {
			if err := q.PutAppLogoImage(ctx, db.PutAppLogoImageParams{AppID: a.ID, ContentType: contentType, Data: data}); err != nil {
				return err
			}
		}
		if body.Mode != "custom" && a.LogoUpdatedAt > 0 {
			if err := q.DeleteAppLogoImage(ctx, a.ID); err != nil {
				return err
			}
			updated = 0
		}
		return q.SetAppLogo(ctx, db.SetAppLogoParams{LogoMode: body.Mode, LogoColor: body.Color, LogoUpdatedAt: updated, ID: a.ID})
	}); err != nil {
		s.internalError(w, r, err)
		return
	}
	a.LogoMode, a.LogoColor, a.LogoUpdatedAt = body.Mode, body.Color, updated
	s.writeApp(w, r, http.StatusOK, a, false)
}

// handleGetAppLogo serves an app's uploaded logo. Logos are not secret: any signed-in user may see them, as
// on member profiles.
func (s *Server) handleGetAppLogo(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	l, err := s.store.GetAppLogoImage(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	serveImage(w, l.ContentType, l.Data)
}

func validColor(c string) bool {
	if len(c) != 7 || c[0] != '#' {
		return false
	}
	_, err := strconv.ParseUint(c[1:], 16, 32)
	return err == nil
}

func jsonDecode(r io.Reader, out any) error {
	return json.NewDecoder(r).Decode(out)
}
