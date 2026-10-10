package agent

import (
	"archive/tar"
	"bytes"
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
	labelSandbox  = "forgeyard.sandbox"
	resyncEvery   = 30 * time.Second
	certResolver  = "letsencrypt"
	dockerSocket  = "/var/run/docker.sock"
	defaultMemory = 512 << 20
)

func containerName(appID int64) string {
	return "forgeyard-app-" + strconv.FormatInt(appID, 10)
}

// sandboxVolume keeps a sandbox's /root across redeployments; it goes with the app.
func sandboxVolume(appID string) string {
	return "forgeyard-sandbox-" + appID
}

// sandboxIdle keeps a sandbox's container running, and stops at once on docker stop.
var sandboxIdle = []string{"sh", "-c", "trap 'exit 0' TERM INT; while :; do sleep 3600 & wait $!; done"}

// Reconciler makes the node's Docker match the desired state sent by the control plane.
type Reconciler struct {
	dc     *docker.Client
	logger *slog.Logger

	mu         sync.Mutex
	desired    *agentpb.DesiredState // nil until the control plane sent one: nothing is removed before
	progress   map[int64]string      // app → "pulling" or "creating" while it happens
	lastError  map[int64]string      // app → why its last deployment failed
	cpuSeen    map[int64]cpuSample   // app → previous CPU counters, to compute use between two reports
	failedOut  map[int64]string      // app → spec hash of a new version that did not start, not retried
	relaysHash string                // the relays last written into Traefik's container
	trigger    chan struct{}
}

// NewReconciler returns a Reconciler; call Run to start it.
func NewReconciler(dc *docker.Client, logger *slog.Logger) *Reconciler {
	return &Reconciler{
		dc: dc, logger: logger, trigger: make(chan struct{}, 1),
		progress: map[int64]string{}, lastError: map[int64]string{}, cpuSeen: map[int64]cpuSample{},
		failedOut: map[int64]string{},
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
	needed := len(d.GetApps()) > 0 || len(d.GetRelays()) > 0
	if err := r.ensureTraefik(ctx, d.GetIngress(), d.GetRelays(), needed); err != nil {
		r.logger.Error("starting Traefik failed", "err", err)
	} else if needed {
		if err := r.syncRelays(ctx, d.GetRelays(), d.GetIngress().GetMode()); err != nil {
			r.logger.Error("updating the relays failed", "err", err)
		}
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
			ct, inspectErr := r.dc.Inspect(ctx, name)
			if err := r.dc.Remove(ctx, name); err != nil {
				r.logger.Error("removing app container failed", "container", name, "err", err)
			} else if inspectErr == nil {
				r.dropBuiltImage(ctx, ct.Config.Image)
				if ct.Config.Labels[labelSandbox] != "" && !strings.HasSuffix(name, "-next") {
					if err := r.dc.RemoveVolume(ctx, sandboxVolume(ct.Config.Labels[labelApp])); err != nil {
						r.logger.Warn("removing a sandbox's volume failed", "app", ct.Config.Labels[labelApp], "err", err)
					}
				}
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
		strconv.FormatInt(app.GetMemoryBytes(), 10), strconv.FormatInt(app.GetGeneration(), 10), ingressMode,
		strconv.FormatBool(app.GetSandbox()))
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
		if app.GetRunning() && ct.State.Running {
			return r.rollOut(ctx, app, hash, ingressMode)
		}
		r.logger.Info("app changed, recreating its container", "app", app.GetName())
		if err := r.dc.Remove(ctx, name); err != nil {
			return err
		}
		if ct.Config.Image != app.GetImage() {
			r.dropBuiltImage(ctx, ct.Config.Image)
		}
		exists = false
	}

	if !exists {
		if !app.GetRunning() {
			return nil // a stopped app needs no container
		}
		defer r.setProgress(app.GetId(), "")
		if err := r.fetchImage(ctx, app); err != nil {
			return err
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

// dropBuiltImage deletes an image the node built from an app's Dockerfile once no container uses it:
// each Dockerfile gets its own tag, which would otherwise pile up. Pulled images are left alone.
func (r *Reconciler) dropBuiltImage(ctx context.Context, image string) {
	if !strings.HasPrefix(image, "forgeyard/") {
		return
	}
	if err := r.dc.RemoveImage(ctx, image); err != nil {
		r.logger.Warn("removing a built image failed", "image", image, "err", err)
	}
}

// fetchImage gets an app's image on the node: built from its Dockerfile, or pulled.
func (r *Reconciler) fetchImage(ctx context.Context, app *agentpb.AppSpec) error {
	if app.GetDockerfile() != "" {
		r.setProgress(app.GetId(), "building")
		if err := r.dc.Build(ctx, app.GetImage(), app.GetDockerfile()); err != nil {
			return fmt.Errorf("construction de l'image : %w", err)
		}
		return nil
	}
	r.setProgress(app.GetId(), "pulling")
	if err := r.dc.Pull(ctx, app.GetImage()); err != nil {
		return fmt.Errorf("téléchargement de l'image %s : %w", app.GetImage(), err)
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
	if app.GetSandbox() {
		id := strconv.FormatInt(app.GetId(), 10)
		labels[labelSandbox] = "true"
		hostConfig["Mounts"] = []map[string]any{{"Type": "volume", "Source": sandboxVolume(id), "Target": "/root"}}
		return map[string]any{
			"Image":            app.GetImage(),
			"Hostname":         app.GetName(),
			"Entrypoint":       []string{""}, // resets the image's own: the idle command runs instead
			"Cmd":              sandboxIdle,
			"WorkingDir":       "/root",
			"Env":              env,
			"Labels":           labels,
			"HostConfig":       hostConfig,
			"NetworkingConfig": map[string]any{"EndpointsConfig": map[string]any{networkName: map[string]any{"Aliases": []string{app.GetName()}}}},
		}
	}
	if host := app.GetHostname(); host != "" {
		// Each version gets its own router, of a higher priority than the one before: Traefik sends every
		// request to the newest version as soon as it sees it, so the old one can stop without losing any.
		router := "forgeyard-app-" + strconv.FormatInt(app.GetId(), 10) + "-" + hash
		labels["traefik.enable"] = "true"
		labels["traefik.docker.network"] = networkName
		labels["traefik.http.routers."+router+".rule"] = "Host(`" + host + "`)"
		labels["traefik.http.routers."+router+".priority"] = strconv.FormatInt(time.Now().Unix(), 10)
		labels["traefik.http.routers."+router+".service"] = router
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
		// The other containers of the node reach it by its app name, e.g. http://grafana:3000.
		"NetworkingConfig": map[string]any{"EndpointsConfig": map[string]any{networkName: map[string]any{"Aliases": []string{app.GetName()}}}},
	}
}

// ensureTraefik runs the ingress proxy that routes app domains to containers. It only runs while the node
// has apps, so an empty node does not hold ports 80/443.
func (r *Reconciler) ensureTraefik(ctx context.Context, ingress *agentpb.Ingress, relays []*agentpb.Relay, needed bool) error {
	if ingress == nil || !needed {
		return r.dc.Remove(ctx, traefikName)
	}
	// "relays" marks the Traefik that reads its relays from a file, so older ones are replaced.
	hash := specHash(ingress.GetMode(), strconv.Itoa(int(ingress.GetHttpPort())), traefikImage, "relays")
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
		// Relays to other nodes come from a file the agent writes into the container.
		"--providers.file.directory=" + relaysDir,
		"--providers.file.watch=true",
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
	// The relays file is there before Traefik starts, so its file provider finds its directory.
	r.mu.Lock()
	r.relaysHash = ""
	r.mu.Unlock()
	if err := r.syncRelays(ctx, relays, ingress.GetMode()); err != nil {
		return err
	}
	r.logger.Info("Traefik started", "mode", ingress.GetMode())
	return r.dc.Start(ctx, traefikName)
}

const (
	relaysDir  = "/forgeyard"
	relaysFile = "relays.yml"
)

// syncRelays writes the relays into Traefik's container when they changed; Traefik reloads the file.
func (r *Reconciler) syncRelays(ctx context.Context, relays []*agentpb.Relay, mode string) error {
	content := relaysConfig(relays, mode)
	hash := specHash(string(content))
	r.mu.Lock()
	same := r.relaysHash == hash
	r.mu.Unlock()
	if same {
		return nil
	}
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	dir := strings.TrimPrefix(relaysDir, "/")
	tw.WriteHeader(&tar.Header{Name: dir + "/", Typeflag: tar.TypeDir, Mode: 0o755})
	tw.WriteHeader(&tar.Header{Name: dir + "/" + relaysFile, Mode: 0o644, Size: int64(len(content))})
	tw.Write(content)
	if err := tw.Close(); err != nil {
		return err
	}
	if err := r.dc.PutArchive(ctx, traefikName, "/", buf.Bytes()); err != nil {
		return err
	}
	r.mu.Lock()
	r.relaysHash = hash
	r.mu.Unlock()
	if len(relays) > 0 {
		r.logger.Info("relays updated", "count", len(relays))
	}
	return nil
}

// relaysConfig is Traefik's dynamic configuration for the relays: one router per app domain, to the
// Traefik of the app's node, keeping the Host header so that Traefik routes it.
func relaysConfig(relays []*agentpb.Relay, mode string) []byte {
	var b strings.Builder
	if len(relays) == 0 {
		return []byte("# No relay: every app of this public IP runs on this node.\n")
	}
	b.WriteString("http:\n  routers:\n")
	name := func(host string) string {
		return "relay-" + strings.Map(func(r rune) rune {
			if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
				return r
			}
			return '-'
		}, host)
	}
	for _, rl := range relays {
		n := name(rl.GetHostname())
		fmt.Fprintf(&b, "    %s:\n      rule: \"Host(`%s`)\"\n      service: %s\n", n, rl.GetHostname(), n)
		if mode == "traefik" {
			fmt.Fprintf(&b, "      entryPoints: [websecure]\n      tls:\n        certResolver: %s\n", certResolver)
		} else {
			b.WriteString("      entryPoints: [web]\n")
		}
	}
	b.WriteString("  services:\n")
	for _, rl := range relays {
		fmt.Fprintf(&b, "    %s:\n      loadBalancer:\n        passHostHeader: true\n        servers:\n          - url: \"%s\"\n", name(rl.GetHostname()), rl.GetTarget())
	}
	return []byte(b.String())
}

type cpuSample struct {
	total, system uint64
}

// sampleUsage fills the CPU and memory use of an app's running container. CPU use needs a previous
// sample, so the first report after a (re)start has none.
func (r *Reconciler) sampleUsage(ctx context.Context, appID int64, st *agentpb.AppStatus) {
	stats, err := r.dc.Stats(ctx, containerName(appID))
	if err != nil {
		return
	}
	st.MemoryUsedBytes = stats.MemoryUsed()
	cur := cpuSample{total: stats.CPUStats.CPUUsage.TotalUsage, system: stats.CPUStats.SystemUsage}
	r.mu.Lock()
	prev, ok := r.cpuSeen[appID]
	r.cpuSeen[appID] = cur
	r.mu.Unlock()
	if ok && cur.total >= prev.total && cur.system > prev.system {
		cpus := max(stats.CPUStats.OnlineCPUs, 1)
		st.CpuPercent = float64(cur.total-prev.total) / float64(cur.system-prev.system) * float64(cpus) * 100
	}
}

// Statuses reports the state of every desired app.
func (r *Reconciler) Statuses(ctx context.Context) []*agentpb.AppStatus {
	d := r.snapshot()
	if d == nil {
		return nil
	}
	out := make([]*agentpb.AppStatus, 0, len(d.GetApps()))
	for _, app := range d.GetApps() {
		if app.GetLeaving() {
			continue // moving to another node, which reports it
		}
		st := &agentpb.AppStatus{AppId: app.GetId()}
		r.mu.Lock()
		progress, lastErr := r.progress[app.GetId()], r.lastError[app.GetId()]
		r.mu.Unlock()

		ct, err := r.dc.Inspect(ctx, containerName(app.GetId()))
		switch {
		case progress != "":
			st.State = progress
		// A failed deployment with the previous version still running is reported with the version that
		// runs, and the error alongside.
		case lastErr != "" && (err != nil || !ct.State.Running):
			st.State, st.Error = "error", lastErr
		case errors.Is(err, docker.ErrNotFound):
			st.State = "stopped"
			if app.GetRunning() {
				st.State = "creating"
			}
		case err != nil:
			st.State, st.Error = "error", err.Error()
		default:
			st.Error = lastErr
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
				r.sampleUsage(ctx, app.GetId(), st)
				st.ListeningPorts = listeningPorts(ct.State.Pid)
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
	r.mu.Lock()
	for id := range r.cpuSeen {
		if !slices.ContainsFunc(d.GetApps(), func(a *agentpb.AppSpec) bool { return a.GetId() == id }) {
			delete(r.cpuSeen, id)
		}
	}
	r.mu.Unlock()
	return out
}

const (
	// readyAfter is how long a new container without a health check must run without restarting before
	// it takes over; readyTimeout is how long it gets to become ready.
	readyAfter   = 5 * time.Second
	readyTimeout = 90 * time.Second
	// drainDelay lets Traefik switch to the new version's router before the old version stops (Traefik
	// applies Docker changes about every 2 seconds).
	drainDelay = 3 * time.Second
)

// rollOut replaces a running app's container without a gap: the new one starts next to the old one (both
// carry the app's Traefik router, so Traefik spreads requests over both), the old one goes once the new one
// is ready, and the new one takes the app's container name. A new version that does not start is removed
// and the old one keeps serving.
func (r *Reconciler) rollOut(ctx context.Context, app *agentpb.AppSpec, hash, ingressMode string) error {
	name := containerName(app.GetId())
	next := name + "-next"
	r.mu.Lock()
	failed := r.failedOut[app.GetId()] == hash
	r.mu.Unlock()
	if failed {
		return errors.New("la nouvelle version n'a pas démarré : l'ancienne reste en ligne (redéployez pour réessayer)")
	}
	r.dc.Remove(ctx, next) // a leftover from an interrupted rollout
	oldImage := ""
	if ct, err := r.dc.Inspect(ctx, name); err == nil {
		oldImage = ct.Config.Image
	}

	defer r.setProgress(app.GetId(), "")
	if err := r.fetchImage(ctx, app); err != nil {
		return err
	}
	r.setProgress(app.GetId(), "deploying")
	if err := r.dc.Create(ctx, next, appContainerConfig(app, hash, ingressMode)); err != nil {
		return fmt.Errorf("création du conteneur : %w", err)
	}
	if err := r.dc.Start(ctx, next); err != nil {
		r.dc.Remove(ctx, next)
		return fmt.Errorf("démarrage de la nouvelle version : %w", err)
	}
	if err := r.waitReady(ctx, next); err != nil {
		r.dc.Remove(ctx, next)
		r.mu.Lock()
		r.failedOut[app.GetId()] = hash
		r.mu.Unlock()
		r.logger.Warn("new version did not start, keeping the old one", "app", app.GetName(), "err", err)
		return fmt.Errorf("la nouvelle version n'a pas démarré (%v) : l'ancienne reste en ligne", err)
	}
	// The new version's router has the higher priority: once Traefik has seen it, the old version gets no
	// more requests, and it stops with its grace period for the ones in flight.
	select {
	case <-ctx.Done():
	case <-time.After(drainDelay):
	}
	if err := r.dc.Stop(ctx, name); err != nil && !errors.Is(err, docker.ErrNotFound) {
		r.logger.Warn("stopping the old version failed", "app", app.GetName(), "err", err)
	}
	if err := r.dc.Remove(ctx, name); err != nil {
		return err
	}
	if err := r.dc.Rename(ctx, next, name); err != nil {
		return err
	}
	if oldImage != app.GetImage() {
		r.dropBuiltImage(ctx, oldImage)
	}
	r.mu.Lock()
	delete(r.failedOut, app.GetId())
	r.mu.Unlock()
	r.logger.Info("app rolled out without downtime", "app", app.GetName(), "image", app.GetImage())
	return nil
}

// waitReady waits for a container to be ready: healthy when its image has a health check, otherwise
// running for readyAfter without restarting.
func (r *Reconciler) waitReady(ctx context.Context, name string) error {
	deadline := time.Now().Add(readyTimeout)
	for {
		ct, err := r.dc.Inspect(ctx, name)
		if err != nil {
			return err
		}
		switch {
		case ct.RestartCount > 0 || ct.State.Status == "exited" || ct.State.Status == "dead":
			if ct.State.OOMKilled {
				return errors.New("manque de mémoire")
			}
			return fmt.Errorf("elle s'est arrêtée, code %d", ct.State.ExitCode)
		case ct.State.Health != nil && ct.State.Health.Status == "healthy":
			return nil
		case ct.State.Health != nil && ct.State.Health.Status == "unhealthy":
			return errors.New("son health check échoue")
		case ct.State.Health == nil && ct.State.Running:
			if t, err := time.Parse(time.RFC3339Nano, ct.State.StartedAt); err == nil && time.Since(t) >= readyAfter {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return errors.New("elle n'est pas prête après 90 secondes")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}
