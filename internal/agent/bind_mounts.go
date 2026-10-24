package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/thomas-bsn/forgeyard/internal/agentpb"
	"github.com/thomas-bsn/forgeyard/internal/docker"
	"google.golang.org/protobuf/proto"
)

// resolveHostPath resolves absolute symlinks in the host namespace. os.Root keeps all filesystem
// accesses confined even if a parent is replaced during traversal; absolute symlink targets restart
// at the host root, rather than the agent container's root.
func resolveHostPath(root *os.Root, name string) (string, error) {
	if !path.IsAbs(name) || strings.ContainsAny(name, "\x00\n\r") {
		return "", errors.New("chemin source absolu requis")
	}
	remaining := strings.Split(path.Clean(name), "/")
	resolved := "/"
	links := 0
	for len(remaining) > 0 {
		component := remaining[0]
		remaining = remaining[1:]
		if component == "" || component == "." {
			continue
		}
		if component == ".." {
			resolved = path.Dir(resolved)
			continue
		}
		candidate := path.Join(resolved, component)
		info, err := root.Lstat(strings.TrimPrefix(candidate, "/"))
		if err != nil {
			return "", fmt.Errorf("source inaccessible %s : %w", name, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			links++
			if links > 40 {
				return "", errors.New("trop de liens symboliques")
			}
			target, err := root.Readlink(strings.TrimPrefix(candidate, "/"))
			if err != nil {
				return "", err
			}
			if path.IsAbs(target) {
				resolved = "/"
			}
			remaining = append(strings.Split(target, "/"), remaining...)
		} else {
			resolved = candidate
		}
	}
	return resolved, nil
}

func pathContains(parent, child string) bool {
	return parent == child || strings.HasPrefix(child, strings.TrimRight(parent, "/")+"/")
}

func protectedHostPaths(ctx context.Context, dc *docker.Client) ([]string, error) {
	if dc == nil {
		return nil, errors.New("Docker n'est pas joignable sur ce node")
	}
	// System control interfaces are never eligible, including directory mounts encompassing them.
	protected := []string{"/proc", "/sys", "/dev", "/run", "/var/run", "/var/lib/docker", "/etc", "/root", "/usr", "/bin", "/sbin", "/boot"}
	if socket := dc.SocketPath(); path.IsAbs(socket) {
		protected = append(protected, path.Clean(socket))
	}
	info, err := dc.Info(ctx)
	if err != nil {
		return nil, fmt.Errorf("vérification du stockage Docker : %w", err)
	}
	if path.IsAbs(info.DockerRootDir) {
		protected = append(protected, path.Clean(info.DockerRootDir))
	}
	all, err := dc.ListAll(ctx)
	if err != nil {
		return nil, fmt.Errorf("vérification des données internes : %w", err)
	}
	for _, container := range all {
		if container.Labels[labelInternal] == "" && container.Labels[labelIngress] == "" {
			continue
		}
		raw, err := dc.InspectRaw(ctx, container.ID)
		if err != nil {
			return nil, err
		}
		mounts, _ := raw["Mounts"].([]any)
		for _, item := range mounts {
			m, _ := item.(map[string]any)
			source, _ := m["Source"].(string)
			destination, _ := m["Destination"].(string)
			// /host is the agent's read-only view of the whole host, not internal data.
			if source != "" && source != "/" && destination != "/host" {
				protected = append(protected, source)
			}
		}
	}
	if os.Getenv("FORGEYARD_HOST_ROOT") == "" {
		for _, p := range []string{"data", "forgeyard-agent-data", os.Getenv("FORGEYARD_AGENT_STATE")} {
			if p != "" {
				if absolute, err := filepath.Abs(p); err == nil {
					protected = append(protected, absolute)
				}
			}
		}
	}
	for _, p := range filepath.SplitList(os.Getenv("FORGEYARD_PROTECTED_HOST_PATHS")) {
		if path.IsAbs(p) {
			protected = append(protected, path.Clean(p))
		}
	}
	return protected, nil
}

func validateHostMounts(ctx context.Context, dc *docker.Client, mounts []*agentpb.BindMount, extraProtected ...string) ([]*agentpb.BindMount, error) {
	if len(mounts) == 0 || len(mounts) > 10 {
		return nil, errors.New("indiquez entre 1 et 10 bind mounts")
	}
	hostRoot := os.Getenv("FORGEYARD_HOST_ROOT")
	if hostRoot == "" {
		hostRoot = "/"
	}
	root, err := os.OpenRoot(hostRoot)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	protected, err := protectedHostPaths(ctx, dc)
	if err != nil {
		return nil, err
	}
	protected = append(protected, extraProtected...)
	// Canonicalize protected paths too: /var/run and other aliases must not weaken the comparison.
	for _, p := range append([]string(nil), protected...) {
		if canonical, err := resolveHostPath(root, p); err == nil {
			protected = append(protected, canonical)
		}
	}
	out := make([]*agentpb.BindMount, 0, len(mounts))
	targets := map[string]bool{}
	for _, m := range mounts {
		canonical, err := resolveHostPath(root, m.GetSource())
		if err != nil {
			return nil, err
		}
		if canonical == "/" {
			return nil, errors.New("le montage de la racine de l'hôte est interdit")
		}
		for _, p := range protected {
			if pathContains(canonical, p) || pathContains(p, canonical) {
				return nil, errors.New("ce chemin donne accès à des données système ou internes protégées")
			}
		}
		// Also recognize native Forgeyard state directories when mounted through a custom location.
		for dir := canonical; dir != "/"; dir = path.Dir(dir) {
			for _, sentinel := range []string{"forgeyard.db", "node.key", "secret.key"} {
				if _, err := root.Stat(strings.TrimPrefix(path.Join(dir, sentinel), "/")); err == nil {
					return nil, errors.New("les données internes de Forgeyard ne peuvent pas être montées")
				}
			}
		}
		target := path.Clean(m.GetTarget())
		if !path.IsAbs(m.GetTarget()) || target == "/" || targets[target] {
			return nil, errors.New("destination de bind mount invalide ou en double")
		}
		targets[target] = true
		copy := proto.Clone(m).(*agentpb.BindMount)
		copy.Source = canonical
		copy.Target = target
		out = append(out, copy)
	}
	return out, nil
}

func (r *Reconciler) checkAppBinds(ctx context.Context, app *agentpb.AppSpec) error {
	if len(app.GetBindMounts()) == 0 {
		return nil
	}
	if !app.GetBindAuthorized() {
		return errors.New("l'autorisation de bind mounts est révoquée ou manquante")
	}
	checked, err := validateHostMounts(ctx, r.dc, app.GetBindMounts(), append(append([]string(nil), r.protectedPaths...), app.GetProtectedHostPaths()...)...)
	if err != nil {
		return err
	}
	for i, m := range checked {
		if m.GetSource() != app.BindMounts[i].GetSource() {
			return errors.New("le chemin approuvé a changé : une nouvelle approbation est nécessaire")
		}
	}
	return nil
}

func bindDockerMounts(app *agentpb.AppSpec) []map[string]any {
	var out []map[string]any
	for _, m := range app.GetBindMounts() {
		out = append(out, map[string]any{"Type": "bind", "Source": m.GetSource(), "Target": m.GetTarget(), "ReadOnly": m.GetReadOnly()})
	}
	return out
}
