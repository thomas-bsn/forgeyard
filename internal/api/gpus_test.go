package api

import (
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/thomas-bsn/forgeyard/internal/agentpb"
)

// reportGpus has a fake agent report its GPU check, and waits until the server has it.
func (f *fakeAgent) reportGpus(t *testing.T, url string, admin *http.Client, g *agentpb.NodeGpus) {
	t.Helper()
	if err := f.stream.Send(&agentpb.AgentMessage{Msg: &agentpb.AgentMessage_NodeGpus{NodeGpus: g}}); err != nil {
		t.Fatal(err)
	}
	for range 100 {
		var list []gpuNodeResponse
		get(t, admin, url+"/api/gpus", &list)
		for _, n := range list {
			if n.NodeID == f.nodeID && n.Status == g.GetStatus() && len(n.GPUs) == len(g.GetGpus()) {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the GPU check never reached the server")
}

// lastSpec reads desired states until one has the app, and returns its spec.
func (f *fakeAgent) lastSpec(t *testing.T, appID int64, ok func(*agentpb.AppSpec) bool) *agentpb.AppSpec {
	t.Helper()
	for range 20 {
		for _, a := range f.desired(t).GetApps() {
			if a.GetId() == appID && ok(a) {
				return a
			}
		}
	}
	t.Fatal("expected spec never came")
	return nil
}

func TestAppGpus(t *testing.T) {
	ts, server := newTestServerWithHandle(t)
	admin := newClient()
	post(t, admin, ts.URL+"/api/setup", setupRequest{Token: testToken, InstanceName: "F", Username: "boss",
		Password: "a-long-enough-password", PublicURL: "https://forgeyard.example.com"})
	gpuNode := connectFakeAgent(t, ts.URL, server, admin, "wsl")
	gpuNode.desired(t)
	const rtx = "GPU-5f3c1a2b-0000-1111-2222-333344445555"

	// Without a GPU, nothing changes.
	var plain appResponse
	if code := postJSON(t, admin, ts.URL+"/api/apps", appInput{Name: "plain", Image: "nginx", Port: 80}, &plain); code != http.StatusCreated || plain.GPU != nil {
		t.Fatalf("app without GPU: %d %+v", code, plain.GPU)
	}
	if spec := gpuNode.lastSpec(t, plain.ID, func(*agentpb.AppSpec) bool { return true }); spec.GetGpu() != nil {
		t.Fatal("an app without GPU got one")
	}

	ollama := appInput{Name: "ollama", Image: "ollama/ollama", Port: 11434, GPU: &gpuRequest{All: true}}
	// The node has not checked its GPUs yet.
	var e errorResponse
	if code := postJSON(t, admin, ts.URL+"/api/apps", ollama, &e); code != http.StatusConflict || !strings.Contains(e.Error, "pas encore vérifié") {
		t.Fatalf("unchecked node: %d %s", code, e.Error)
	}
	// Docker cannot give GPUs.
	gpuNode.reportGpus(t, ts.URL, admin, &agentpb.NodeGpus{Status: "unsupported", Error: "pas de NVIDIA Container Toolkit"})
	if code := postJSON(t, admin, ts.URL+"/api/apps", ollama, &e); code != http.StatusConflict || !strings.Contains(e.Error, "NVIDIA Container Toolkit") {
		t.Fatalf("node without GPU support: %d %s", code, e.Error)
	}

	gpuNode.reportGpus(t, ts.URL, admin, &agentpb.NodeGpus{Status: "ok", Gpus: []*agentpb.Gpu{
		{Vendor: "nvidia", Index: 0, Id: rtx, Name: "NVIDIA GeForce RTX 4070 SUPER", MemoryBytes: 12282 << 20}}})

	// Bad requests.
	for _, bad := range []*gpuRequest{{Vendor: "amd", All: true}, {}, {IDs: []string{"GPU 1"}}, {IDs: []string{rtx, rtx}}} {
		in := ollama
		in.GPU = bad
		if code := postJSON(t, admin, ts.URL+"/api/apps", in, nil); code != http.StatusBadRequest {
			t.Errorf("%+v accepted: %d", bad, code)
		}
	}
	unknown := ollama
	unknown.GPU = &gpuRequest{IDs: []string{"GPU-nope"}}
	if code := postJSON(t, admin, ts.URL+"/api/apps", unknown, &e); code != http.StatusConflict || !strings.Contains(e.Error, "n'a pas ce GPU") {
		t.Fatalf("unknown GPU: %d %s", code, e.Error)
	}

	// All the GPUs of the node.
	var app appResponse
	if code := postJSON(t, admin, ts.URL+"/api/apps", ollama, &app); code != http.StatusCreated || app.GPU == nil || !app.GPU.All || app.GPU.Vendor != "nvidia" {
		t.Fatalf("all GPUs: %d %+v", code, app.GPU)
	}
	spec := gpuNode.lastSpec(t, app.ID, func(*agentpb.AppSpec) bool { return true })
	if g := spec.GetGpu(); g == nil || !g.GetAll() || g.GetVendor() != "nvidia" {
		t.Fatalf("spec: %v", g)
	}
	get(t, admin, ts.URL+"/api/apps/"+itoa(app.ID), &app)
	if !slices.Equal(app.GPUNames, []string{"NVIDIA GeForce RTX 4070 SUPER"}) {
		t.Fatalf("names: %v", app.GPUNames)
	}

	// Its observability has the GPU's use; an app without GPU has none.
	if err := gpuNode.stream.Send(&agentpb.AgentMessage{Msg: &agentpb.AgentMessage_Metrics{Metrics: &agentpb.Metrics{
		Gpus: []*agentpb.GpuMetric{{Id: rtx, UtilizationPercent: 87, MemoryUsedBytes: 7 << 30, MemoryTotalBytes: 12 << 30}}}}}); err != nil {
		t.Fatal(err)
	}
	var usage struct {
		Gpus []struct {
			Name    string `json:"name"`
			Samples []struct {
				Util float64 `json:"util"`
				Mem  int64   `json:"mem"`
			} `json:"samples"`
		} `json:"gpus"`
	}
	for range 100 {
		get(t, admin, ts.URL+"/api/apps/"+itoa(app.ID)+"/usage", &usage)
		if len(usage.Gpus) == 1 && len(usage.Gpus[0].Samples) == 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(usage.Gpus) != 1 || usage.Gpus[0].Name != "NVIDIA GeForce RTX 4070 SUPER" || len(usage.Gpus[0].Samples) != 1 || usage.Gpus[0].Samples[0].Util != 87 {
		t.Fatalf("GPU usage: %+v", usage)
	}
	usage.Gpus = nil
	get(t, admin, ts.URL+"/api/apps/"+itoa(plain.ID)+"/usage", &usage)
	if len(usage.Gpus) != 0 {
		t.Fatalf("an app without GPU shows one: %+v", usage.Gpus)
	}

	// Redeploying keeps it: a new generation, the same request.
	postJSON(t, admin, ts.URL+"/api/apps/"+itoa(app.ID)+"/redeploy", struct{}{}, nil)
	spec = gpuNode.lastSpec(t, app.ID, func(a *agentpb.AppSpec) bool { return a.GetGeneration() > spec.GetGeneration() })
	if !spec.GetGpu().GetAll() {
		t.Fatal("redeploy lost the GPU")
	}

	// One GPU by its ID, then none: each saved, each sent.
	one := ollama
	one.GPU = &gpuRequest{IDs: []string{rtx}}
	if code := putJSON(t, admin, ts.URL+"/api/apps/"+itoa(app.ID), one, &app); code != http.StatusOK || app.GPU.All || !slices.Equal(app.GPU.IDs, []string{rtx}) {
		t.Fatalf("one GPU: %d %+v", code, app.GPU)
	}
	spec = gpuNode.lastSpec(t, app.ID, func(a *agentpb.AppSpec) bool { return len(a.GetGpu().GetDeviceIds()) == 1 })
	if spec.GetGpu().GetAll() || spec.GetGpu().GetDeviceIds()[0] != rtx {
		t.Fatalf("one GPU spec: %v", spec.GetGpu())
	}
	none := ollama
	none.GPU = nil
	var cleared appResponse
	if code := putJSON(t, admin, ts.URL+"/api/apps/"+itoa(app.ID), none, &cleared); code != http.StatusOK || cleared.GPU != nil {
		t.Fatalf("no GPU: %d %+v", code, cleared.GPU)
	}
	gpuNode.lastSpec(t, app.ID, func(a *agentpb.AppSpec) bool { return a.GetGpu() == nil })

	// With another node, a GPU app goes to the one that has a GPU, and cannot move to the other.
	cpuNode := connectFakeAgent(t, ts.URL, server, admin, "pi")
	cpuNode.desired(t)
	cpuNode.reportGpus(t, ts.URL, admin, &agentpb.NodeGpus{Status: "unsupported", Error: "pas de GPU"})
	second := ollama
	second.Name = "ollama-2"
	var app2 appResponse
	if code := postJSON(t, admin, ts.URL+"/api/apps", second, &app2); code != http.StatusCreated || app2.NodeID != gpuNode.nodeID {
		t.Fatalf("GPU app placed on node %d (want %d): %d", app2.NodeID, gpuNode.nodeID, code)
	}
	if code := postJSON(t, admin, ts.URL+"/api/admin/apps/"+itoa(app2.ID)+"/move", map[string]any{"nodeId": cpuNode.nodeID}, &e); code != http.StatusConflict || !strings.Contains(e.Error, "GPU") {
		t.Fatalf("move to a node without GPU: %d %s", code, e.Error)
	}
	pinned := second
	pinned.Name, pinned.NodeID = "ollama-3", cpuNode.nodeID
	if code := postJSON(t, admin, ts.URL+"/api/apps", pinned, &e); code != http.StatusConflict {
		t.Fatalf("GPU app on a chosen node without GPU: %d", code)
	}
}
