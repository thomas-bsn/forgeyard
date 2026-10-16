package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"slices"
	"strings"

	"github.com/thomas-bsn/forgeyard/internal/agentpb"
	"github.com/thomas-bsn/forgeyard/internal/nodes"
	"github.com/thomas-bsn/forgeyard/internal/store/db"
)

// An app may ask for GPUs of its node: all of them, or some by ID. It is access, not a reservation: other
// apps may use the same GPUs and their memory. Only NVIDIA for now; the vendor is kept with the request.

const vendorNvidia = "nvidia"

var gpuIDPattern = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)

// gpuRequest is the GPUs an app may use.
type gpuRequest struct {
	Vendor string   `json:"vendor"`
	All    bool     `json:"all"`
	IDs    []string `json:"ids,omitempty"`
}

// validate checks a request's form, before the node is known.
func (g *gpuRequest) validate() string {
	if g.Vendor == "" {
		g.Vendor = vendorNvidia
	}
	if g.Vendor != vendorNvidia {
		return "GPU : seuls les GPU NVIDIA sont pris en charge pour l'instant"
	}
	if g.All {
		g.IDs = nil
		return ""
	}
	if len(g.IDs) == 0 {
		return "GPU : choisissez tous les GPU du node ou au moins un GPU"
	}
	if len(g.IDs) > 8 {
		return "GPU : 8 au maximum"
	}
	for i, id := range g.IDs {
		if !gpuIDPattern.MatchString(id) || slices.Contains(g.IDs[:i], id) {
			return "GPU : identifiant invalide ou en double : " + id
		}
	}
	return ""
}

// nodeGpus is what a node's agent last found: its GPUs, and whether Docker gives them to containers.
type nodeGpus struct {
	Status string      `json:"status"` // ok, unsupported, error, or "" when not checked yet
	Error  string      `json:"error,omitempty"`
	GPUs   []nodes.GPU `json:"gpus"`
}

func gpusOf(n db.Node) nodeGpus {
	out := nodeGpus{Status: n.GpuStatus, Error: n.GpuError, GPUs: []nodes.GPU{}}
	json.Unmarshal([]byte(n.Gpus), &out.GPUs)
	return out
}

// errGpu is a GPU request a node cannot meet, said to the user as is.
type errGpu string

func (e errGpu) Error() string { return string(e) }

// checkGpuNode tells whether a node can give an app the GPUs it asks for.
func checkGpuNode(n db.Node, g *gpuRequest) error {
	if g == nil {
		return nil
	}
	ng := gpusOf(n)
	switch ng.Status {
	case "ok":
	case "":
		return errGpu("le node " + n.Name + " n'a pas encore vérifié ses GPU : réessayez dans un instant")
	case "unsupported":
		return errGpu("le node " + n.Name + " ne peut pas donner de GPU aux conteneurs : " + ng.Error)
	default:
		return errGpu("la vérification des GPU du node " + n.Name + " a échoué : " + ng.Error)
	}
	if g.All {
		return nil
	}
	for _, id := range g.IDs {
		if !slices.ContainsFunc(ng.GPUs, func(x nodes.GPU) bool { return x.ID == id && x.Vendor == g.Vendor }) {
			if len(ng.GPUs) == 0 {
				return errGpu("le node " + n.Name + " n'a pas pu lister ses GPU : choisissez « tous les GPU du node »")
			}
			return errGpu("le node " + n.Name + " n'a pas ce GPU : " + id)
		}
	}
	return nil
}

// pickGpuNode chooses the node of a new app that asks for GPUs: the chosen one if it can, else the
// recommended node among those that can.
func (s *Server) pickGpuNode(ctx context.Context, chosen int64, g *gpuRequest) (db.Node, error) {
	if chosen != 0 {
		n, err := s.pickNode(ctx, chosen)
		if err != nil {
			return n, err
		}
		return n, checkGpuNode(n, g)
	}
	choices, err := s.nodeChoices(ctx)
	if err != nil {
		return db.Node{}, err
	}
	if len(choices) == 0 {
		return db.Node{}, errNoNode
	}
	var first error
	for _, c := range choices {
		n, err := s.store.GetNode(ctx, c.ID)
		if err != nil {
			return db.Node{}, err
		}
		if err := checkGpuNode(n, g); err == nil {
			return n, nil
		} else if first == nil {
			first = err
		}
	}
	if len(choices) == 1 {
		return db.Node{}, first
	}
	return db.Node{}, errGpu("aucun node en ligne ne peut donner ce GPU aux conteneurs")
}

func appGpu(a db.App) *gpuRequest {
	if a.Gpu == "" {
		return nil
	}
	var g gpuRequest
	if json.Unmarshal([]byte(a.Gpu), &g) != nil {
		return nil
	}
	return &g
}

func sealGpu(g *gpuRequest) string {
	if g == nil {
		return ""
	}
	return string(must(json.Marshal(g)))
}

func (g *gpuRequest) spec() *agentpb.GpuRequest {
	if g == nil {
		return nil
	}
	return &agentpb.GpuRequest{Vendor: g.Vendor, All: g.All, DeviceIds: g.IDs}
}

// gpuNodeResponse is a node's GPUs, for the app form: the online nodes, their GPUs, and why not.
type gpuNodeResponse struct {
	NodeID   int64  `json:"nodeId"`
	NodeName string `json:"nodeName"`
	nodeGpus
}

// handleListGpus lists the online nodes' GPUs, for the app form. Users see them too: their app may go to
// the node that has one.
func (s *Server) handleListGpus(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.ListNodes(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	out := []gpuNodeResponse{}
	for _, n := range list {
		if _, online := s.nodes.Live(n.ID); !online || n.Status != "active" {
			continue
		}
		out = append(out, gpuNodeResponse{NodeID: n.ID, NodeName: n.Name, nodeGpus: gpusOf(n)})
	}
	writeJSON(w, http.StatusOK, out)
}

// writeGpuError answers a GPU request a node cannot meet; it reports whether err was one.
func writeGpuError(w http.ResponseWriter, err error) bool {
	var ge errGpu
	if !errors.As(err, &ge) {
		return false
	}
	writeError(w, http.StatusConflict, strings.TrimSpace(string(ge)))
	return true
}

// gpuUsage is a GPU an app may use and its use over the last hour, the GPU's as a whole.
type gpuUsage struct {
	nodes.GPU
	Samples []nodes.GpuSample `json:"samples"`
}

// appGpuUsage lists the use of the GPUs an app may use: none for an app without GPU.
func (s *Server) appGpuUsage(ctx context.Context, a db.App) ([]gpuUsage, error) {
	out := []gpuUsage{}
	g := appGpu(a)
	if g == nil {
		return out, nil
	}
	node, err := s.store.GetNode(ctx, a.NodeID)
	if err != nil {
		return nil, err
	}
	for _, x := range gpusOf(node).GPUs {
		if g.All || slices.Contains(g.IDs, x.ID) {
			samples := s.nodes.GpuUsage(node.ID, x.ID)
			if samples == nil {
				samples = []nodes.GpuSample{}
			}
			out = append(out, gpuUsage{GPU: x, Samples: samples})
		}
	}
	return out, nil
}
