package compose

import (
	"math"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// The memory a service may be given, in MiB, as for an app.
const (
	MinMemoryMB = 64
	MaxMemoryMB = 16384
)

// memorySources gathers the memory limits a service sets: x-forgeyard.memory, and mem_limit or
// deploy.resources.limits.memory as docker compose reads them, so that the same file limits a service the
// same way on a laptop. Two different limits are an error rather than one silently winning.
type memorySources struct {
	service string
	mb      int
	from    string
}

func (m *memorySources) add(p *parser, n *yaml.Node, from string, mb int) {
	if m.from != "" && m.mb != mb {
		p.errorf(n, "%s.%s : %d Mo, alors que %s indique %d Mo ; gardez une seule limite", m.service, from, mb, m.from, m.mb)
		return
	}
	m.mb, m.from = mb, from
}

// memorySizePattern is a size as docker compose takes it: bytes without a unit, or b, k, m, g with an
// optional b, in any case.
var memorySizePattern = regexp.MustCompile(`^([0-9]+(?:\.[0-9]+)?)\s*([bkmg]?)b?$`)

// ParseMemorySize reads a docker compose size ("512m", "1g", "1.5GB", 536870912) as MiB, rounded up.
func ParseMemorySize(s string) (int, bool) {
	m := memorySizePattern.FindStringSubmatch(strings.ToLower(strings.TrimSpace(s)))
	if m == nil {
		return 0, false
	}
	n, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0, false
	}
	unit := map[string]float64{"": 1, "b": 1, "k": 1 << 10, "m": 1 << 20, "g": 1 << 30}[m[2]]
	mb := math.Ceil(n * unit / (1 << 20))
	if mb > math.MaxInt32 {
		return 0, false
	}
	return int(mb), true
}

func (p *parser) memorySize(n *yaml.Node, what string) (int, bool) {
	raw := p.scalar(n, what)
	if raw == "" {
		return 0, false
	}
	mb, ok := ParseMemorySize(raw)
	switch {
	case !ok:
		p.errorf(n, "%s : une taille, par exemple 512m ou 1g", what)
	case mb < MinMemoryMB || mb > MaxMemoryMB:
		hint := ""
		if _, err := strconv.Atoi(raw); err == nil {
			hint = " (sans unité, c'est en octets : écrivez " + raw + "m pour des Mo)"
		}
		p.errorf(n, "%s : entre 64m et 16g%s", what, hint)
		ok = false
	}
	return mb, ok
}

// deploy reads deploy.resources.limits.memory; the rest of deploy is for Swarm, which Forgeyard is not.
func (p *parser) deploy(n *yaml.Node, name string, mem *memorySources) {
	const only = "seul deploy.resources.limits.memory est pris en charge"
	path := []string{"resources", "limits", "memory"}
	what := name + ".deploy"
	for depth := 0; ; depth++ {
		if n.Kind != yaml.MappingNode {
			p.errorf(n, "%s : un objet est attendu", what)
			return
		}
		var next *yaml.Node
		for _, kv := range pairs(n) {
			if kv.key != path[depth] {
				p.errorf(kv.keyN, "%s.%s n'est pas pris en charge : %s", what, kv.key, only)
				continue
			}
			next = kv.value
		}
		if next == nil {
			return
		}
		what += "." + path[depth]
		if depth == len(path)-1 {
			if mb, ok := p.memorySize(next, what); ok {
				mem.add(p, next, "deploy.resources.limits.memory", mb)
			}
			return
		}
		n = next
	}
}
