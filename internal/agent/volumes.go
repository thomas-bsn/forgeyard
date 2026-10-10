package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/thomas-bsn/forgeyard/internal/agentpb"
	"github.com/thomas-bsn/forgeyard/internal/docker"
)

// An app's volumes keep the paths of its container across redeployments. Each is a Docker volume named
// after the app and the path, so the next container of the app mounts the same one. They are only
// deleted when the server says the app is gone (DropAppData): a container removed for any other reason
// (a move, a desired state received wrong) leaves its data in place.

const labelVolumePath = "forgeyard.volume.path"

// volumeName is the Docker volume of an app's path: readable, and unique per path.
func volumeName(appID int64, path string) string {
	var slug strings.Builder
	for _, r := range strings.ToLower(strings.Trim(path, "/")) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			slug.WriteRune(r)
		} else if s := slug.String(); s != "" && !strings.HasSuffix(s, "-") {
			slug.WriteByte('-')
		}
	}
	s := strings.Trim(slug.String(), "-")
	if len(s) > 40 {
		s = s[:40]
	}
	sum := sha256.Sum256([]byte(path))
	return "forgeyard-app-" + strconv.FormatInt(appID, 10) + "-" + s + "-" + hex.EncodeToString(sum[:3])
}

// volumeMounts mounts an app's volumes, labeled with the app so they can be found and deleted with it.
func volumeMounts(app *agentpb.AppSpec) []map[string]any {
	id := strconv.FormatInt(app.GetId(), 10)
	var out []map[string]any
	for _, p := range app.GetVolumes() {
		out = append(out, map[string]any{
			"Type": "volume", "Source": volumeName(app.GetId(), p), "Target": p,
			"VolumeOptions": map[string]any{"Labels": map[string]string{labelApp: id, labelVolumePath: p}},
		})
	}
	return out
}

// dropAppData removes a deleted app's containers, if still there, then its volumes.
func dropAppData(ctx context.Context, dc *docker.Client, appID int64) error {
	id := strconv.FormatInt(appID, 10)
	for _, name := range []string{containerName(appID), containerName(appID) + "-next"} {
		if err := dc.Remove(ctx, name); err != nil {
			return err
		}
	}
	vols, err := dc.ListVolumes(ctx, "label", labelApp+"="+id)
	if err != nil {
		return err
	}
	names := []string{sandboxVolume(id)} // created before volumes were labeled
	for _, v := range vols {
		names = append(names, v.Name)
	}
	for _, n := range names {
		if err := dc.RemoveVolume(ctx, n); err != nil {
			return err
		}
	}
	return nil
}

// volumeSizes measures the apps' volumes at most once a minute: Docker walks their files to do it.
type volumeSizes struct {
	mu   sync.Mutex
	at   time.Time
	last []*agentpb.VolumeUsage
}

const volumeSizesEvery = time.Minute

func (v *volumeSizes) get(ctx context.Context, dc *docker.Client) []*agentpb.VolumeUsage {
	v.mu.Lock()
	defer v.mu.Unlock()
	if time.Since(v.at) < volumeSizesEvery {
		return v.last
	}
	v.at = time.Now()
	vols, err := dc.VolumeSizes(ctx)
	if err != nil {
		return v.last
	}
	var out []*agentpb.VolumeUsage
	for _, vol := range vols {
		appID, err := strconv.ParseInt(vol.Labels[labelApp], 10, 64)
		if err != nil {
			if !strings.HasPrefix(vol.Name, "forgeyard-sandbox-") {
				continue
			}
			appID, _ = strconv.ParseInt(strings.TrimPrefix(vol.Name, "forgeyard-sandbox-"), 10, 64)
			vol.Labels = map[string]string{labelVolumePath: "/root"}
		}
		size := int64(-1)
		if vol.UsageData != nil {
			size = vol.UsageData.Size
		}
		out = append(out, &agentpb.VolumeUsage{Name: vol.Name, AppId: appID, Path: vol.Labels[labelVolumePath], SizeBytes: size})
	}
	v.last = out
	return out
}
