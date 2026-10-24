package agent

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/thomas-bsn/forgeyard/internal/agentpb"
	"github.com/thomas-bsn/forgeyard/internal/docker"
)

// The services of a compose stack are apps with a StackService: they run on the stack's own network,
// where they reach each other by service name, mount the stack's named volumes, and start once the
// services they depend on are ready. A job (a service another one waits to complete, like a database
// migration) runs once per version and is never restarted.

const (
	labelStack      = "forgeyard.stack"
	labelVolumeName = "forgeyard.volume.name" // a stack volume's name in the compose file
	stackNetPrefix  = "forgeyard-stack-"

	// waitRetry is how soon a service waiting for its dependencies is looked at again.
	waitRetry = 2 * time.Second
)

// Dependency conditions, as docker compose names them.
const (
	condStarted   = "service_started"
	condHealthy   = "service_healthy"
	condCompleted = "service_completed_successfully"
)

// waitState is why a service of a stack has not started: it waits, or one of its dependencies failed.
type waitState struct {
	reason  string
	blocked bool
}

// orderApps puts each service of a stack after the services it depends on, so that one reconciliation
// goes as far as it can. Other apps keep their order.
func orderApps(apps []*agentpb.AppSpec) []*agentpb.AppSpec {
	byID := map[int64]*agentpb.AppSpec{}
	for _, a := range apps {
		byID[a.GetId()] = a
	}
	out := make([]*agentpb.AppSpec, 0, len(apps))
	state := map[int64]int{} // 1 visiting, 2 placed
	var visit func(a *agentpb.AppSpec)
	visit = func(a *agentpb.AppSpec) {
		if state[a.GetId()] != 0 {
			return // placed, or a loop the server should have refused
		}
		state[a.GetId()] = 1
		for _, d := range a.GetStack().GetDependsOn() {
			if dep, ok := byID[d.GetAppId()]; ok {
				visit(dep)
			}
		}
		state[a.GetId()] = 2
		out = append(out, a)
	}
	for _, a := range apps {
		visit(a)
	}
	return out
}

// dependencyReady tells whether a dependency's container meets a condition. ct is nil when it has no
// container. failed is set when it never will: a job that ended with an error, or a health check missing.
func dependencyReady(d *agentpb.Dependency, ct *docker.Container) (ready bool, waiting, failed string) {
	name := d.GetService()
	if ct == nil {
		return false, "attend le démarrage de " + name, ""
	}
	switch d.GetCondition() {
	case condCompleted:
		switch {
		case ct.State.Running || ct.State.Status == "created" || ct.State.Status == "restarting":
			return false, "attend la fin du job " + name, ""
		case ct.State.ExitCode == 0 && !ct.State.OOMKilled:
			return true, "", ""
		case ct.State.OOMKilled:
			return false, "", "le job " + name + " a échoué : manque de mémoire"
		}
		return false, "", fmt.Sprintf("le job %s a échoué (code %d) : voir ses logs", name, ct.State.ExitCode)
	case condHealthy:
		switch {
		case !ct.State.Running:
			return false, "attend que " + name + " tourne", ""
		case ct.State.Health == nil:
			return false, "", name + " n'a pas de healthcheck : la condition service_healthy ne peut pas être remplie"
		case ct.State.Health.Status == "healthy":
			return true, "", ""
		}
		return false, "attend que " + name + " soit healthy (" + ct.State.Health.Status + ")", ""
	}
	// service_started: it was started, even if it already ended.
	if ct.State.Running || ct.State.Status == "exited" || ct.State.Status == "restarting" {
		return true, "", ""
	}
	return false, "attend le démarrage de " + name, ""
}

// waitFor checks a service's dependencies on the node.
func (r *Reconciler) waitFor(ctx context.Context, app *agentpb.AppSpec) (waitState, error) {
	var waiting []string
	for _, d := range app.GetStack().GetDependsOn() {
		var ct *docker.Container
		c, err := r.dc.Inspect(ctx, containerName(d.GetAppId()))
		switch {
		case err == nil:
			ct = &c
		case !errors.Is(err, docker.ErrNotFound):
			return waitState{}, err
		}
		ready, why, failed := dependencyReady(d, ct)
		if failed != "" {
			return waitState{reason: failed, blocked: true}, nil
		}
		if !ready {
			waiting = append(waiting, why)
		}
	}
	return waitState{reason: strings.Join(waiting, ", ")}, nil
}

func (r *Reconciler) setWaiting(appID int64, w waitState) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if w.reason == "" {
		delete(r.waiting, appID)
	} else {
		r.waiting[appID] = w
	}
}

// stackConfig adapts a service's container to its stack: network and name, command, health check,
// restart policy and named volumes.
func stackConfig(app *agentpb.AppSpec, config, hostConfig map[string]any, labels map[string]string) {
	st := app.GetStack()
	labels[labelStack] = strconv.FormatInt(st.GetStackId(), 10)
	hostConfig["NetworkMode"] = st.GetNetwork()
	config["NetworkingConfig"] = map[string]any{"EndpointsConfig": map[string]any{
		st.GetNetwork(): map[string]any{"Aliases": []string{st.GetService()}},
	}}
	if len(st.GetEntrypoint()) > 0 {
		config["Entrypoint"] = st.GetEntrypoint()
	}
	if len(st.GetCommand()) > 0 {
		config["Cmd"] = st.GetCommand()
	}
	if st.GetUser() != "" {
		config["User"] = st.GetUser()
	}
	if h := st.GetHealthcheck(); h != nil {
		config["Healthcheck"] = map[string]any{
			"Test": h.GetTest(), "Interval": h.GetIntervalNs(), "Timeout": h.GetTimeoutNs(),
			"StartPeriod": h.GetStartPeriodNs(), "Retries": h.GetRetries(),
		}
	}
	hostConfig["RestartPolicy"] = restartPolicy(st)
	var mounts []map[string]any
	for _, v := range st.GetVolumes() {
		mounts = append(mounts, map[string]any{
			"Type": "volume", "Source": v.GetName(), "Target": v.GetTarget(), "ReadOnly": v.GetReadOnly(),
			"VolumeOptions": map[string]any{"Labels": map[string]string{labelStack: labels[labelStack], labelVolumeName: v.GetVolume()}},
		})
	}
	if len(mounts) > 0 {
		hostConfig["Mounts"] = mounts
	}
}

// restartPolicy is Docker's form of a service's restart setting: unless-stopped by default, never for a job.
func restartPolicy(st *agentpb.StackService) map[string]any {
	if st.GetJob() {
		return map[string]any{"Name": "no"}
	}
	name, count, _ := strings.Cut(st.GetRestart(), ":")
	switch name {
	case "":
		return map[string]any{"Name": "unless-stopped"}
	case "on-failure":
		n, _ := strconv.Atoi(count)
		return map[string]any{"Name": "on-failure", "MaximumRetryCount": n}
	}
	return map[string]any{"Name": name}
}

// createContainer creates an app's container. An exposed service of a stack joins Forgeyard's network
// too, where Traefik reaches it and other apps know it by its app name.
func (r *Reconciler) createContainer(ctx context.Context, name string, app *agentpb.AppSpec, hash, ingressMode string) error {
	if err := r.checkAppBinds(ctx, app); err != nil {
		return err
	}
	if err := r.dc.Create(ctx, name, appContainerConfig(app, hash, ingressMode)); err != nil {
		return fmt.Errorf("création du conteneur : %w", err)
	}
	if app.GetStack() != nil && app.GetHostname() != "" {
		if err := r.dc.ConnectNetwork(ctx, networkName, name, app.GetName()); err != nil {
			r.dc.Remove(ctx, name)
			return fmt.Errorf("connexion au réseau forgeyard : %w", err)
		}
	}
	return nil
}

// ensureStackNetworks creates the networks of the stacks of the node, and deletes those of stacks that are
// gone (Docker refuses while a container still uses one: it goes at a later pass).
func (r *Reconciler) ensureStackNetworks(ctx context.Context, apps []*agentpb.AppSpec) {
	wanted := map[string]bool{}
	for _, a := range apps {
		if n := a.GetStack().GetNetwork(); n != "" && !wanted[n] {
			wanted[n] = true
			if err := r.dc.EnsureNetwork(ctx, n); err != nil {
				r.logger.Error("creating a stack network failed", "network", n, "err", err)
			}
		}
	}
	nets, err := r.dc.ListNetworks(ctx)
	if err != nil {
		return
	}
	for _, n := range nets {
		if strings.HasPrefix(n.Name, stackNetPrefix) && !wanted[n.Name] {
			if err := r.dc.RemoveNetwork(ctx, n.Name); err == nil {
				r.logger.Info("stack network removed", "network", n.Name)
			}
		}
	}
}

// dropStack deletes what a deleted stack left: its containers, its network, and its volumes when asked.
func dropStack(ctx context.Context, dc *docker.Client, stackID int64, volumes bool) error {
	id := strconv.FormatInt(stackID, 10)
	names, err := dc.ListByLabel(ctx, labelStack+"="+id)
	if err != nil {
		return err
	}
	for _, n := range names {
		if err := removeContainer(ctx, dc, n); err != nil {
			return err
		}
	}
	if err := dc.RemoveNetwork(ctx, stackNetPrefix+id); err != nil {
		return err
	}
	if !volumes {
		return nil
	}
	vols, err := dc.ListVolumes(ctx, "label", labelStack+"="+id)
	if err != nil {
		return err
	}
	var errs []error
	for _, v := range vols {
		if err := dc.RemoveVolume(ctx, v.Name); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// removeContainer removes a container and waits until it is gone: the reconciliation may be removing it
// already, and its volumes can only go after it.
func removeContainer(ctx context.Context, dc *docker.Client, name string) error {
	err := dc.Remove(ctx, name)
	if err == nil {
		return nil
	}
	deadline := time.Now().Add(time.Minute)
	for time.Now().Before(deadline) {
		if _, ierr := dc.Inspect(ctx, name); errors.Is(ierr, docker.ErrNotFound) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	return err
}

// jobState is how a job's ended container is reported: completed, or failed with its exit code.
func jobState(ct docker.Container) string {
	if ct.State.ExitCode == 0 && !ct.State.OOMKilled {
		return "completed"
	}
	return "failed"
}
