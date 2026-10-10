package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/thomas-bsn/forgeyard/internal/docker"
)

// When a node is removed, its agent deletes what Forgeyard put on the machine: the apps' containers and
// volumes, Traefik and the forgeyard network. Then a short-lived container of its image deletes the
// agent's own container and identity volume, as a container cannot delete itself. The containers
// Forgeyard did not create (Pi-hole, a compose project…) are left alone.

const removerName = "forgeyard-agent-remover"

// uninstall deletes the apps, their data, Traefik and the network of the node.
func uninstall(ctx context.Context, dc *docker.Client) error {
	var errs []error
	apps, err := dc.ListByLabel(ctx, labelApp)
	if err != nil {
		return err
	}
	for _, name := range append(apps, traefikName, helperName, updaterName) {
		if err := dc.Remove(ctx, name); err != nil {
			errs = append(errs, fmt.Errorf("conteneur %s : %w", name, err))
		}
	}
	vols, err := dc.ListVolumes(ctx, "label", labelApp)
	if err != nil {
		return err
	}
	sandboxes, err := dc.ListVolumes(ctx, "name", "forgeyard-sandbox-")
	if err != nil {
		return err
	}
	for _, v := range append(vols, sandboxes...) {
		if err := dc.RemoveVolume(ctx, v.Name); err != nil {
			errs = append(errs, fmt.Errorf("volume %s : %w", v.Name, err))
		}
	}
	if err := dc.RemoveNetwork(ctx, networkName); err != nil {
		errs = append(errs, fmt.Errorf("réseau %s : %w", networkName, err))
	}
	return errors.Join(errs...)
}

// removeSelf starts the container that deletes the agent's own container and identity volume. An agent
// Docker Compose manages, or one run outside a container, is left in place: it is disconnected anyway.
func removeSelf(ctx context.Context, dc *docker.Client, stateDir string) error {
	self, err := selfContainer(ctx, dc)
	if err != nil {
		return nil // not in a container
	}
	if self.Config.Labels["com.docker.compose.project"] != "" {
		return nil
	}
	state := ""
	for _, m := range self.Mounts {
		if m.Type == "volume" && m.Destination == stateDir {
			state = m.Name
		}
	}
	var binds []string
	for _, b := range self.HostConfig.Binds {
		if strings.Contains(b, "docker.sock") {
			binds = append(binds, b)
		}
	}
	dc.Remove(ctx, removerName)
	if err := dc.Create(ctx, removerName, map[string]any{
		"Image":      self.Config.Image,
		"Entrypoint": []string{"forgeyard-agent"},
		"Cmd":        []string{"remove-self", self.ID, state},
		"Labels":     map[string]string{labelInternal: "helper"},
		"HostConfig": map[string]any{"Binds": binds, "AutoRemove": true},
	}); err != nil {
		return err
	}
	return dc.Start(ctx, removerName)
}

// RemoveSelf deletes an agent's container and its identity volume: run by the remover.
func RemoveSelf(ctx context.Context, dc *docker.Client, container, volume string) error {
	if err := dc.Remove(ctx, container); err != nil {
		return err
	}
	if volume == "" {
		return nil
	}
	return dc.RemoveVolume(ctx, volume)
}
