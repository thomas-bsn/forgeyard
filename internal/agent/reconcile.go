package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/thomas-bsn/forgeyard/internal/agentpb"
	"github.com/thomas-bsn/forgeyard/internal/docker"
)

const (
	networkName   = "forgeyard"
	traefikName   = "forgeyard-traefik"
	traefikImage  = "traefik:v3.6"
	labelApp      = "forgeyard.app"
	labelSpec     = "forgeyard.spec"
	labelIngress  = "forgeyard.ingress"
	resyncEvery   = 30 * time.Second
	certResolver  = "letsencrypt"
	dockerSocket  = "/var/run/docker.sock"
	defaultMemory = 512 << 20
)

func containerName(appID int64) string {
	return "forgeyard-app-" + strconv.FormatInt(appID, 10)
}

// Reconciler makes the node's Docker match the desired state sent by the control plane.
type Reconciler struct {
	dc     *docker.Client
	logger *slog.Logger

	mu        sync.Mutex
	desired   *agentpb.DesiredState // nil until the control plane sent one: nothing is removed before
	progress  map[int64]string      // app → "pulling" or "creating" while it happens
	lastError map[int64]string      // app → why its last deployment failed
	trigger   chan struct{}
}

// NewReconciler returns a Reconciler; call Run to start it.
func NewReconciler(dc *docker.Client, logger *slog.Logger) *Reconciler {
	return &Reconciler{
		dc: dc, logger: logger, trigger: make(chan struct{}, 1),
		progress: map[int64]string{}, lastError: map[int64]string{},
	}
}

// SetDesired replaces the desired state and reconciles soon.
func (r *Reconciler) SetDesired(d *agentpb.DesiredState) {
	r.mu.Lock()
	r.desired = d
	r.mu.Unlock()
	select {
	case r.trigger <- struct{}{}:
	default:
	}
}

// Run reconciles on every change and every 30 seconds, until ctx ends.
func (r *Reconciler) Run(ctx context.Context) {
	ticker := time.NewTicker(resyncEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-r.trigger:
		case <-ticker.C:
		}
		r.reconcile(ctx)
	}
}

func (r *Reconciler) snapshot() *agentpb.DesiredState {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.desired == nil {
		return nil
	}
	return proto.Clone(r.desired).(*agentpb.DesiredState)
}

func (r *Reconciler) setProgress(appID int64, step string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if step == "" {
		delete(r.progress, appID)
	} else {
		r.progress[appID] = step
	}
}

func (r *Reconciler) setError(appID int64, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err == nil {
		delete(r.lastError, appID)
	} else {
		r.lastError[appID] = err.Error()
	}
}

func (r *Reconciler) reconcile(ctx context.Context) {
	d := r.snapshot()
	if d == nil {
		return
	}
	if err := r.dc.EnsureNetwork(ctx, networkName); err != nil {
		r.logger.Error("creating the forgeyard network failed", "err", err)
		return
	}
	if err := r.ensureTraefik(ctx, d.GetIngress(), len(d.GetApps()) > 0); err != nil {
		r.logger.Error("starting Traefik failed", "err", err)
	}

	wanted := map[string]bool{}
	for _, app := range d.GetApps() {
		wanted[containerName(app.GetId())] = true
		err := r.ensureApp(ctx, app, d.GetIngress().GetMode())
		r.setError(app.GetId(), err)
		if err != nil {
			r.logger.Error("deploying app failed", "app", app.GetName(), "err", err)
		}
	}

	existing, err := r.dc.ListByLabel(ctx, labelApp)
	if err != nil {
		r.logger.Error("listing app containers failed", "err", err)
		return
	}
	for _, name := range existing {
		if !wanted[name] {
			r.logger.Info("removing app container", "container", name)
			if err := r.dc.Remove(ctx, name); err != nil {
				r.logger.Error("removing app container failed", "container", name, "err", err)
			}
		}
	}
}

// specHash identifies what a container was created from: a different hash means it must be recreated.
func specHash(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:8])
}

func appHash(app *agentpb.AppSpec, ingressMode string) string {
	env := make([]string, 0, len(app.GetEnv()))
	for k, v := range app.GetEnv() {
		env = append(env, k+"="+v)
	}
	slices.Sort(env)
	return specHash(app.GetImage(), strconv.Itoa(int(app.GetPort())), strings.Join(env, "\n"), app.GetHostname(),
		strconv.FormatInt(app.GetMemoryBytes(), 10), strconv.FormatInt(app.GetGeneration(), 10), ingressMode)
}

func (r *Reconciler) ensureApp(ctx context.Context, app *agentpb.AppSpec, ingressMode string) error {
	name := containerName(app.GetId())
	hash := appHash(app, ingressMode)

	ct, err := r.dc.Inspect(ctx, name)
	exists := err == nil
	if err != nil && !errors.Is(err, docker.ErrNotFound) {
		return err
	}
	if exists && ct.Config.Labels[labelSpec] != hash {
		r.logger.Info("app changed, recreating its container", "app", app.GetName())
		if err := r.dc.Remove(ctx, name); err != nil {
			return err
		}
		exists = false
	}

	if !exists {
		if !app.GetRunning() {
			return nil // a stopped app needs no container
		}
		r.setProgress(app.GetId(), "pulling")
		defer r.setProgress(app.GetId(), "")
		if err := r.dc.Pull(ctx, app.GetImage()); err != nil {
			return fmt.Errorf("téléchargement de l'image %s : %w", app.GetImage(), err)
		}
		r.setProgress(app.GetId(), "creating")
		if err := r.dc.Create(ctx, name, appContainerConfig(app, hash, ingressMode)); err != nil {
			return fmt.Errorf("création du conteneur : %w", err)
		}
		if ct, err = r.dc.Inspect(ctx, name); err != nil {
			return err
		}
		r.logger.Info("app container created", "app", app.GetName(), "image", app.GetImage())
	}

	switch {
	// Only a fresh container is started here: one that exited is a crash (Docker restarts it itself under
	// the unless-stopped policy) or was stopped on purpose; starting again goes through a new generation.
	case app.GetRunning() && ct.State.Status == "created":
		return r.dc.Start(ctx, name)
	case !app.GetRunning() && (ct.State.Running || ct.State.Status == "restarting"):
		return r.dc.Stop(ctx, name)
	}
	return nil
}

func appContainerConfig(app *agentpb.AppSpec, hash, ingressMode string) map[string]any {
	port := strconv.Itoa(int(app.GetPort())) + "/tcp"
	env := make([]string, 0, len(app.GetEnv()))
	for k, v := range app.GetEnv() {
		env = append(env, k+"="+v)
	}
	memory := app.GetMemoryBytes()
	if memory <= 0 {
		memory = defaultMemory
	}
	labels := map[string]string{
		labelApp:  strconv.FormatInt(app.GetId(), 10),
		labelSpec: hash,
	}
	hostConfig := map[string]any{
		"NetworkMode":   networkName,
		"RestartPolicy": map[string]any{"Name": "unless-stopped"},
		"Memory":        memory,
		"MemorySwap":    memory, // no swap beyond the limit
		// The isolation rules of the architecture: no privileges, no host mounts, no host network.
		"Privileged":  false,
		"SecurityOpt": []string{"no-new-privileges:true"},
		"LogConfig":   map[string]any{"Type": "json-file", "Config": map[string]string{"max-size": "10m", "max-file": "3"}},
	}
	if host := app.GetHostname(); host != "" {
		router := "forgeyard-app-" + strconv.FormatInt(app.GetId(), 10)
		labels["traefik.enable"] = "true"
		labels["traefik.docker.network"] = networkName
		labels["traefik.http.routers."+router+".rule"] = "Host(`" + host + "`)"
		labels["traefik.http.services."+router+".loadbalancer.server.port"] = strconv.Itoa(int(app.GetPort()))
		if ingressMode == "traefik" {
			labels["traefik.http.routers."+router+".entrypoints"] = "websecure"
			labels["traefik.http.routers."+router+".tls.certresolver"] = certResolver
		} else {
			labels["traefik.http.routers."+router+".entrypoints"] = "web"
		}
	} else {
		// No domain: publish the port on a free port of the node.
		hostConfig["PortBindings"] = map[string]any{port: []map[string]string{{"HostPort": ""}}}
	}
	return map[string]any{
		"Image":        app.GetImage(),
		"Env":          env,
		"Labels":       labels,
		"ExposedPorts": map[string]any{port: map[string]any{}},
		"HostConfig":   hostConfig,
	}
}

// ensureTraefik runs the ingress proxy that routes app domains to containers. It is only started once the
// node has apps, so an empty node does not take ports 80/443.
func (r *Reconciler) ensureTraefik(ctx context.Context, ingress *agentpb.Ingress, needed bool) error {
	if ingress == nil || !needed {
		return nil
	}
	hash := specHash(ingress.GetMode(), strconv.Itoa(int(ingress.GetHttpPort())), traefikImage)
	ct, err := r.dc.Inspect(ctx, traefikName)
	if err == nil && ct.Config.Labels[labelIngress] == hash {
		if !ct.State.Running && ct.State.Status != "restarting" {
			return r.dc.Start(ctx, traefikName)
		}
		return nil
	}
	if err != nil && !errors.Is(err, docker.ErrNotFound) {
		return err
	}
	if err == nil {
		if err := r.dc.Remove(ctx, traefikName); err != nil {
			return err
		}
	}

	args := []string{
		"--providers.docker=true",
		"--providers.docker.exposedByDefault=false",
		"--providers.docker.network=" + networkName,
		"--entryPoints.web.address=:80",
		"--log.level=WARN",
	}
	ports := map[string]any{}
	exposed := map[string]any{"80/tcp": map[string]any{}}
	binds := []string{dockerSocket + ":" + dockerSocket + ":ro"}
	if ingress.GetMode() == "traefik" {
		args = append(args,
			"--entryPoints.websecure.address=:443",
			"--entryPoints.web.http.redirections.entryPoint.to=websecure",
			"--entryPoints.web.http.redirections.entryPoint.scheme=https",
			"--certificatesResolvers."+certResolver+".acme.tlsChallenge=true",
			"--certificatesResolvers."+certResolver+".acme.storage=/acme/acme.json",
		)
		ports["80/tcp"] = []map[string]string{{"HostPort": "80"}}
		ports["443/tcp"] = []map[string]string{{"HostPort": "443"}}
		exposed["443/tcp"] = map[string]any{}
		binds = append(binds, "forgeyard-traefik-acme:/acme")
	} else {
		port := ingress.GetHttpPort()
		if port == 0 {
			port = 8090
		}
		ports["80/tcp"] = []map[string]string{{"HostPort": strconv.Itoa(int(port))}}
	}

	if err := r.dc.Pull(ctx, traefikImage); err != nil {
		return err
	}
	if err := r.dc.Create(ctx, traefikName, map[string]any{
		"Image":        traefikImage,
		"Cmd":          args,
		"Labels":       map[string]string{labelIngress: hash},
		"ExposedPorts": exposed,
		"HostConfig": map[string]any{
			"NetworkMode":   networkName,
			"RestartPolicy": map[string]any{"Name": "unless-stopped"},
			"PortBindings":  ports,
			"Binds":         binds,
		},
	}); err != nil {
		return err
	}
	r.logger.Info("Traefik started", "mode", ingress.GetMode())
	return r.dc.Start(ctx, traefikName)
}

// Statuses reports the state of every desired app.
func (r *Reconciler) Statuses(ctx context.Context) []*agentpb.AppStatus {
	d := r.snapshot()
	if d == nil {
		return nil
	}
	out := make([]*agentpb.AppStatus, 0, len(d.GetApps()))
	for _, app := range d.GetApps() {
		st := &agentpb.AppStatus{AppId: app.GetId()}
		r.mu.Lock()
		progress, lastErr := r.progress[app.GetId()], r.lastError[app.GetId()]
		r.mu.Unlock()

		ct, err := r.dc.Inspect(ctx, containerName(app.GetId()))
		switch {
		case progress != "":
			st.State = progress
		case lastErr != "":
			st.State, st.Error = "error", lastErr
		case errors.Is(err, docker.ErrNotFound):
			st.State = "stopped"
			if app.GetRunning() {
				st.State = "creating"
			}
		case err != nil:
			st.State, st.Error = "error", err.Error()
		default:
			st.ExitCode = int32(ct.State.ExitCode)
			st.OomKilled = ct.State.OOMKilled
			st.RestartCount = int32(ct.RestartCount)
			if t, err := time.Parse(time.RFC3339Nano, ct.State.StartedAt); err == nil && t.Year() > 1 {
				st.StartedAt = t.Unix()
			}
			if b := ct.NetworkSettings.Ports[strconv.Itoa(int(app.GetPort()))+"/tcp"]; len(b) > 0 {
				if p, err := strconv.Atoi(b[0].HostPort); err == nil {
					st.HostPort = int32(p)
				}
			}
			switch ct.State.Status {
			case "running":
				st.State = "running"
			case "restarting":
				st.State = "restarting"
			case "created":
				st.State = "creating"
			default:
				st.State = "exited"
				if !app.GetRunning() {
					st.State = "stopped"
				}
			}
		}
		out = append(out, st)
	}
	return out
}
