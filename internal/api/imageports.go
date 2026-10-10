package api

import (
	"context"
	"net/http"
	"time"

	"github.com/thomas-bsn/forgeyard/internal/registry"
)

// The app form fills in the port an image exposes and offers the paths it keeps (its VOLUMEs): read from
// its registry, without pulling it, and remembered for an hour.

const imagePortsTTL = time.Hour

type imagePorts struct {
	ports   []int
	volumes []string
	err     string
	at      time.Time
}

type imagePortsResponse struct {
	Ports   []int    `json:"ports"`
	Volumes []string `json:"volumes"`
	Error   string   `json:"error,omitempty"` // why the image could not be read (private, unknown…)
}

func (s *Server) handleImagePorts(w http.ResponseWriter, r *http.Request) {
	image := r.URL.Query().Get("image")
	if _, err := registry.ParseRef(image); err != nil || len(image) > 255 {
		writeError(w, http.StatusBadRequest, "image invalide")
		return
	}
	s.mu.Lock()
	cached, ok := s.imagePorts[image]
	s.mu.Unlock()
	ttl := imagePortsTTL
	if cached.err != "" {
		ttl = time.Minute // a failure may pass: a rate limit, a registry down
	}
	if !ok || time.Since(cached.at) > ttl {
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		cfg, err := registry.ImageConfig(ctx, s.registryClient, image)
		cancel()
		cached = imagePorts{ports: cfg.Ports, volumes: cfg.Volumes, at: time.Now()}
		if err != nil {
			cached.err = err.Error()
			s.logger.Warn("reading an image's ports failed", "image", image, "err", err)
		}
		s.mu.Lock()
		if len(s.imagePorts) > 1000 {
			clear(s.imagePorts)
		}
		s.imagePorts[image] = cached
		s.mu.Unlock()
	}
	ports := cached.ports
	if ports == nil {
		ports = []int{}
	}
	volumes := cached.volumes
	if volumes == nil {
		volumes = []string{}
	}
	writeJSON(w, http.StatusOK, imagePortsResponse{Ports: ports, Volumes: volumes, Error: cached.err})
}
