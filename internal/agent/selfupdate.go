package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/thomas-bsn/forgeyard/internal/docker"
)

// An agent updates itself by handing over to its next version: it pulls the new image, then starts a
// short-lived container of that image ("forgeyard-agent replace"), which stops the agent's container and
// recreates it with the same settings and the new image. Apps keep running meanwhile. A container managed
// by Docker Compose is left to Compose.

const updaterName = "forgeyard-agent-updater"

// selfContainer is the container the agent runs in: Docker sets its hostname to its short ID.
func selfContainer(ctx context.Context, dc *docker.Client) (docker.Container, error) {
	host, err := os.Hostname()
	if err != nil {
		return docker.Container{}, err
	}
	return dc.Inspect(ctx, host)
}

// canSelfUpdate reports whether the agent runs in a container it may replace.
func canSelfUpdate(ctx context.Context, dc *docker.Client) bool {
	if dc == nil {
		return false
	}
	self, err := selfContainer(ctx, dc)
	return err == nil && self.Config.Labels["com.docker.compose.project"] == ""
}

// startUpdate pulls image and starts the container that replaces this agent's.
func startUpdate(ctx context.Context, dc *docker.Client, image string) error {
	self, err := selfContainer(ctx, dc)
	if err != nil {
		return fmt.Errorf("l'agent ne tourne pas dans un conteneur qu'il peut remplacer : %w", err)
	}
	if self.Config.Labels["com.docker.compose.project"] != "" {
		return errors.New("cet agent est géré par Docker Compose : mettez-le à jour avec docker compose")
	}
	if err := dc.Pull(ctx, image); err != nil {
		return fmt.Errorf("téléchargement de %s : %w", image, err)
	}
	// The updater reaches Docker through the same socket as the agent.
	var binds []string
	for _, b := range self.HostConfig.Binds {
		if strings.Contains(b, "docker.sock") {
			binds = append(binds, b)
		}
	}
	dc.Remove(ctx, updaterName) // a leftover from an interrupted update
	if err := dc.Create(ctx, updaterName, map[string]any{
		"Image":      image,
		"Entrypoint": []string{"forgeyard-agent"},
		"Cmd":        []string{"replace", self.ID, image},
		"Labels":     map[string]string{labelInternal: "helper"},
		"HostConfig": map[string]any{"Binds": binds, "AutoRemove": true},
	}); err != nil {
		return err
	}
	return dc.Start(ctx, updaterName)
}

// Replace recreates a container with another image and the same settings: run by the updater, with the
// new version of the agent. When the new container does not start, the old one comes back.
func Replace(ctx context.Context, dc *docker.Client, old, image string) error {
	raw, err := dc.InspectRaw(ctx, old)
	if err != nil {
		return err
	}
	name, _ := raw["Name"].(string)
	name = strings.TrimPrefix(name, "/")
	cfg, _ := raw["Config"].(map[string]any)
	host, _ := raw["HostConfig"].(map[string]any)
	if name == "" || cfg == nil || host == nil {
		return errors.New("inspection incomplète du conteneur à remplacer")
	}
	body := map[string]any{}
	for k, v := range cfg {
		body[k] = v
	}
	body["Image"] = image
	// A new container gets its own hostname: the agent finds itself through it.
	delete(body, "Hostname")
	delete(body, "Domainname")
	body["HostConfig"] = host
	endpoints := map[string]any{}
	if ns, ok := raw["NetworkSettings"].(map[string]any); ok {
		if nets, ok := ns["Networks"].(map[string]any); ok {
			for net, v := range nets {
				ep := map[string]any{}
				if e, ok := v.(map[string]any); ok && e["Aliases"] != nil {
					ep["Aliases"] = e["Aliases"]
				}
				endpoints[net] = ep
			}
		}
	}
	body["NetworkingConfig"] = map[string]any{"EndpointsConfig": endpoints}

	backup := name + "-previous"
	dc.Remove(ctx, backup)
	if err := dc.Stop(ctx, old); err != nil && !errors.Is(err, docker.ErrNotFound) {
		return err
	}
	if err := dc.Rename(ctx, old, backup); err != nil {
		dc.Start(ctx, old)
		return err
	}
	restore := func(cause error) error {
		dc.Remove(context.Background(), name)
		dc.Rename(context.Background(), backup, name)
		dc.Start(context.Background(), name)
		return cause
	}
	if err := dc.Create(ctx, name, body); err != nil {
		return restore(fmt.Errorf("création du nouveau conteneur : %w", err))
	}
	if err := dc.Start(ctx, name); err != nil {
		return restore(fmt.Errorf("démarrage du nouveau conteneur : %w", err))
	}
	// The new version must stay up a moment, or the previous one comes back.
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(5 * time.Second):
	}
	if ct, err := dc.Inspect(ctx, name); err != nil || !ct.State.Running {
		return restore(errors.New("la nouvelle version s'est arrêtée aussitôt"))
	}
	return dc.Remove(ctx, backup)
}
