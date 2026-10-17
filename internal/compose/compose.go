// Package compose reads the part of docker-compose.yml Forgeyard runs as a stack: services from published
// images, with their command, environment, named volumes, start order and health checks. Everything else
// is refused with the line it is on, so the same file keeps working with `docker compose up` on a laptop.
package compose

import (
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Error is a problem of the file, at a line (0 when it is about the whole file).
type Error struct {
	Line    int    `json:"line"`
	Message string `json:"message"`
}

// Errors are all the problems found, by line.
type Errors []Error

func (e Errors) Error() string {
	parts := make([]string, len(e))
	for i, x := range e {
		if x.Line > 0 {
			parts[i] = fmt.Sprintf("ligne %d : %s", x.Line, x.Message)
		} else {
			parts[i] = x.Message
		}
	}
	return strings.Join(parts, "\n")
}

// Dependency conditions, as docker compose names them.
const (
	Started   = "service_started"
	Healthy   = "service_healthy"
	Completed = "service_completed_successfully"
)

// Stack is a parsed compose file.
type Stack struct {
	Services []Service // dependencies first
	Volumes  []string  // the named volumes, sorted
}

// Service is one container of the stack.
type Service struct {
	Name  string `json:"-"`
	Image string `json:"-"`
	// Command and Entrypoint replace the image's own when set.
	Command     []string          `json:"command,omitempty"`
	Entrypoint  []string          `json:"entrypoint,omitempty"`
	Environment map[string]string `json:"-"`
	Volumes     []Mount           `json:"volumes,omitempty"`
	DependsOn   []Dependency      `json:"dependsOn,omitempty"`
	Healthcheck *Healthcheck      `json:"healthcheck,omitempty"`
	// Restart is no, always, on-failure[:N] or unless-stopped; empty for Forgeyard's default.
	Restart string `json:"restart,omitempty"`
	// Job: another service waits for it to complete. It runs once and is not restarted.
	Job bool `json:"job,omitempty"`
	// Expose is the port it serves at the stack's address; 0 for a service only the stack reaches.
	Expose   int    `json:"-"`
	Backup   string `json:"backup,omitempty"` // "postgres": backed up with pg_dump instead of its files
	MemoryMB int    `json:"-"`                // 0 for the default
}

// Mount puts a named volume of the stack in a service's container.
type Mount struct {
	Volume   string `json:"volume"`
	Target   string `json:"target"`
	ReadOnly bool   `json:"readOnly,omitempty"`
}

// Dependency is a service that must reach a condition before this one starts.
type Dependency struct {
	Service   string `json:"service"`
	Condition string `json:"condition"`
}

// Healthcheck tells when a service is ready.
type Healthcheck struct {
	Test        []string      `json:"test"` // CMD or CMD-SHELL first, as Docker wants it
	Interval    time.Duration `json:"interval,omitempty"`
	Timeout     time.Duration `json:"timeout,omitempty"`
	StartPeriod time.Duration `json:"startPeriod,omitempty"`
	Retries     int           `json:"retries,omitempty"`
}

var (
	serviceNamePattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,30}[a-z0-9])?$`)
	volumeNamePattern  = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,62}$`)
	envKeyPattern      = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	restartPattern     = regexp.MustCompile(`^(no|always|unless-stopped|on-failure(:[0-9]+)?)$`)
)

// refused explains the keys of a service people often write that Forgeyard does not take.
var refused = map[string]string{
	"build":          "build n'est pas pris en charge : utilisez une image publiée (image: …)",
	"ports":          "ports n'est pas pris en charge : exposez le service avec x-forgeyard: { expose: { port: 80 } }",
	"expose":         "expose n'est pas nécessaire : les services du projet se joignent par leur nom",
	"env_file":       "env_file n'est pas pris en charge : mettez les variables dans le projet et utilisez ${VAR}",
	"networks":       "networks n'est pas pris en charge : le projet a son propre réseau, les services s'y joignent par leur nom",
	"container_name": "container_name n'est pas pris en charge : Forgeyard nomme les conteneurs",
	"network_mode":   "network_mode n'est pas pris en charge",
	"privileged":     "privileged n'est pas autorisé",
	"cap_add":        "cap_add n'est pas autorisé",
	"devices":        "devices n'est pas autorisé",
	"extends":        "extends n'est pas pris en charge",
}

// Parse reads a compose file, replacing ${VAR} with the stack's variables. All the problems are returned
// at once. When the file is readable but refused (an unsupported key, a missing variable), the services it
// could read come with the errors, in start order, so an editor can preview them; the stack is nil only
// when nothing could be read at all (invalid YAML, no services).
func Parse(src []byte, vars map[string]string) (*Stack, Errors) {
	p := &parser{vars: vars}
	var doc yaml.Node
	if err := yaml.Unmarshal(src, &doc); err != nil {
		return nil, Errors{{Line: yamlErrorLine(err), Message: "YAML invalide : " + yamlErrorText(err)}}
	}
	if len(doc.Content) == 0 {
		return nil, Errors{{Message: "le fichier est vide"}}
	}
	root := resolve(doc.Content[0])
	if root.Kind != yaml.MappingNode {
		return nil, Errors{{Line: root.Line, Message: "le fichier doit être un objet avec services:"}}
	}
	p.interpolate(root)
	st := p.stack(root)
	if len(p.errs) > 0 {
		sort.SliceStable(p.errs, func(i, j int) bool { return p.errs[i].Line < p.errs[j].Line })
		return st, p.errs
	}
	return st, nil
}

type parser struct {
	vars map[string]string
	errs Errors
}

func (p *parser) errorf(n *yaml.Node, format string, args ...any) {
	line := 0
	if n != nil {
		line = n.Line
	}
	p.errs = append(p.errs, Error{Line: line, Message: fmt.Sprintf(format, args...)})
}

// pair is a key of a mapping with its value.
type pair struct {
	key   string
	keyN  *yaml.Node
	value *yaml.Node
}

// pairs lists a mapping's keys in order, with YAML merge keys (<<: *anchor) applied: the mapping's own
// keys win over merged ones.
func pairs(n *yaml.Node) []pair {
	n = resolve(n)
	var own, merged []pair
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], resolve(n.Content[i+1])
		if k.Value == "<<" && k.Tag == "!!merge" {
			sources := []*yaml.Node{v}
			if v.Kind == yaml.SequenceNode {
				sources = v.Content
			}
			for _, src := range sources {
				if src = resolve(src); src.Kind == yaml.MappingNode {
					merged = append(merged, pairs(src)...)
				}
			}
			continue
		}
		own = append(own, pair{k.Value, k, v})
	}
	out := own
	for _, m := range merged {
		if !slices.ContainsFunc(out, func(o pair) bool { return o.key == m.key }) {
			out = append(out, m)
		}
	}
	return out
}

func resolve(n *yaml.Node) *yaml.Node {
	for n != nil && n.Kind == yaml.AliasNode {
		n = n.Alias
	}
	return n
}

func isNull(n *yaml.Node) bool {
	return n == nil || (n.Kind == yaml.ScalarNode && n.Tag == "!!null")
}

func (p *parser) stack(root *yaml.Node) *Stack {
	st := &Stack{}
	var services *yaml.Node
	volumes := map[string]bool{}
	for _, kv := range pairs(root) {
		switch {
		case kv.key == "services":
			services = kv.value
		case kv.key == "volumes":
			p.topVolumes(kv.value, volumes)
		case kv.key == "version" || kv.key == "name" || strings.HasPrefix(kv.key, "x-"):
			// version is obsolete, name is the stack's own; x- keys hold anchors.
		case kv.key == "networks":
			p.errorf(kv.keyN, "networks n'est pas pris en charge : le projet a son propre réseau")
		default:
			p.errorf(kv.keyN, "%s n'est pas pris en charge", kv.key)
		}
	}
	if services == nil || services.Kind != yaml.MappingNode || len(services.Content) == 0 {
		p.errorf(root, "services: est obligatoire, avec au moins un service")
		return nil
	}
	for _, kv := range pairs(services) {
		if !serviceNamePattern.MatchString(kv.key) {
			p.errorf(kv.keyN, "nom de service %q : 1 à 32 caractères parmi a-z, 0-9 et -", kv.key)
			continue
		}
		st.Services = append(st.Services, p.service(kv.key, kv.keyN, kv.value, volumes))
	}
	for name := range volumes {
		st.Volumes = append(st.Volumes, name)
	}
	sort.Strings(st.Volumes)
	p.links(st, services)
	return st
}

func (p *parser) topVolumes(n *yaml.Node, out map[string]bool) {
	if isNull(n) {
		return
	}
	if n.Kind != yaml.MappingNode {
		p.errorf(n, "volumes: doit lister des volumes nommés")
		return
	}
	for _, kv := range pairs(n) {
		if !volumeNamePattern.MatchString(kv.key) {
			p.errorf(kv.keyN, "nom de volume %q invalide", kv.key)
			continue
		}
		out[kv.key] = true
		if isNull(kv.value) {
			continue
		}
		if kv.value.Kind != yaml.MappingNode {
			p.errorf(kv.value, "volume %s : seul un volume vide ({}) est pris en charge", kv.key)
			continue
		}
		for _, opt := range pairs(kv.value) {
			p.errorf(opt.keyN, "volume %s : %s n'est pas pris en charge (Forgeyard crée et nomme le volume)", kv.key, opt.key)
		}
	}
}

func (p *parser) service(name string, keyN, n *yaml.Node, volumes map[string]bool) Service {
	svc := Service{Name: name, Environment: map[string]string{}}
	if n.Kind != yaml.MappingNode {
		p.errorf(n, "service %s : doit être un objet", name)
		return svc
	}
	var imageN *yaml.Node
	for _, kv := range pairs(n) {
		v := kv.value
		switch kv.key {
		case "image":
			imageN = v
			svc.Image = p.scalar(v, name+".image")
		case "command":
			svc.Command = p.words(v, name+".command")
		case "entrypoint":
			svc.Entrypoint = p.words(v, name+".entrypoint")
		case "environment":
			p.environment(v, name, svc.Environment)
		case "volumes":
			svc.Volumes = p.mounts(v, name, volumes)
		case "depends_on":
			svc.DependsOn = p.dependsOn(v, name)
		case "healthcheck":
			svc.Healthcheck = p.healthcheck(v, name)
		case "restart":
			svc.Restart = p.scalar(v, name+".restart")
			if svc.Restart != "" && !restartPattern.MatchString(svc.Restart) {
				p.errorf(v, "%s.restart : no, always, on-failure ou unless-stopped", name)
			}
		case "x-forgeyard":
			p.forgeyard(v, &svc)
		default:
			switch msg, ok := refused[kv.key]; {
			case ok:
				p.errorf(kv.keyN, "%s : %s", name, msg)
			case strings.HasPrefix(kv.key, "x-"):
			default:
				p.errorf(kv.keyN, "%s.%s n'est pas pris en charge", name, kv.key)
			}
		}
	}
	if imageN == nil {
		p.errorf(keyN, "service %s : image est obligatoire (une image publiée, build n'est pas pris en charge)", name)
	} else if svc.Image != "" && (len(svc.Image) > 255 || strings.ContainsAny(svc.Image, " \t\n")) {
		p.errorf(imageN, "%s.image invalide : par exemple postgres:17 ou ghcr.io/moi/api:main", name)
	}
	return svc
}

func (p *parser) scalar(n *yaml.Node, what string) string {
	if n.Kind != yaml.ScalarNode || isNull(n) {
		p.errorf(n, "%s : une valeur simple est attendue", what)
		return ""
	}
	return n.Value
}

// words reads a command: a list, or a string split like a shell would (without running one).
func (p *parser) words(n *yaml.Node, what string) []string {
	switch {
	case isNull(n):
		return nil
	case n.Kind == yaml.ScalarNode:
		w, err := SplitWords(n.Value)
		if err != nil {
			p.errorf(n, "%s : %v", what, err)
		}
		return w
	case n.Kind == yaml.SequenceNode:
		out := []string{}
		for _, item := range n.Content {
			out = append(out, p.scalar(resolve(item), what))
		}
		return out
	}
	p.errorf(n, "%s : une chaîne ou une liste est attendue", what)
	return nil
}

func (p *parser) environment(n *yaml.Node, svc string, out map[string]string) {
	set := func(keyN *yaml.Node, key string, value *string) {
		if !envKeyPattern.MatchString(key) {
			p.errorf(keyN, "%s : nom de variable invalide : %s", svc, key)
			return
		}
		if value == nil {
			// KEY alone takes the stack's variable of that name, as compose takes the shell's.
			if v, ok := p.vars[key]; ok {
				out[key] = v
			}
			return
		}
		out[key] = *value
	}
	switch n.Kind {
	case yaml.MappingNode:
		for _, kv := range pairs(n) {
			if isNull(kv.value) {
				set(kv.keyN, kv.key, nil)
			} else if kv.value.Kind != yaml.ScalarNode {
				p.errorf(kv.value, "%s : la variable %s doit être une valeur simple", svc, kv.key)
			} else {
				v := kv.value.Value
				set(kv.keyN, kv.key, &v)
			}
		}
	case yaml.SequenceNode:
		for _, item := range n.Content {
			item = resolve(item)
			s := p.scalar(item, svc+".environment")
			if k, v, ok := strings.Cut(s, "="); ok {
				set(item, k, &v)
			} else if s != "" {
				set(item, s, nil)
			}
		}
	default:
		if !isNull(n) {
			p.errorf(n, "%s.environment : un objet ou une liste KEY=valeur est attendu", svc)
		}
	}
}

func (p *parser) mounts(n *yaml.Node, svc string, volumes map[string]bool) []Mount {
	if n.Kind != yaml.SequenceNode {
		p.errorf(n, "%s.volumes : une liste est attendue", svc)
		return nil
	}
	var out []Mount
	for _, item := range n.Content {
		item = resolve(item)
		var m Mount
		switch item.Kind {
		case yaml.ScalarNode:
			parts := strings.Split(item.Value, ":")
			if len(parts) == 1 {
				p.errorf(item, "%s : volume anonyme %s : nommez-le (donnees:%s) et déclarez-le sous volumes:", svc, item.Value, item.Value)
				continue
			}
			if len(parts) > 3 || (len(parts) == 3 && parts[2] != "ro" && parts[2] != "rw") {
				p.errorf(item, "%s : volume %q : nom:/chemin ou nom:/chemin:ro", svc, item.Value)
				continue
			}
			m = Mount{Volume: parts[0], Target: parts[1], ReadOnly: len(parts) == 3 && parts[2] == "ro"}
		case yaml.MappingNode:
			kind := "volume"
			for _, kv := range pairs(item) {
				switch kv.key {
				case "type":
					kind = p.scalar(kv.value, svc+".volumes.type")
				case "source":
					m.Volume = p.scalar(kv.value, svc+".volumes.source")
				case "target":
					m.Target = p.scalar(kv.value, svc+".volumes.target")
				case "read_only":
					m.ReadOnly = kv.value.Value == "true"
				default:
					p.errorf(kv.keyN, "%s.volumes.%s n'est pas pris en charge", svc, kv.key)
				}
			}
			if kind != "volume" {
				p.errorf(item, "%s : volume de type %s refusé : seuls les volumes nommés sont pris en charge", svc, kind)
				continue
			}
		default:
			p.errorf(item, "%s.volumes : nom:/chemin attendu", svc)
			continue
		}
		switch {
		case m.Volume == "" || strings.HasPrefix(m.Volume, "/") || strings.HasPrefix(m.Volume, ".") || strings.HasPrefix(m.Volume, "~"):
			p.errorf(item, "%s : %s est un dossier de la machine : seuls les volumes nommés sont pris en charge (pas de bind mount)", svc, m.Volume)
		case !volumes[m.Volume]:
			p.errorf(item, "%s : le volume %s doit être déclaré sous volumes: à la racine du fichier", svc, m.Volume)
		case !strings.HasPrefix(m.Target, "/"):
			p.errorf(item, "%s : %s doit être monté sur un chemin absolu", svc, m.Volume)
		default:
			out = append(out, m)
		}
	}
	return out
}

func (p *parser) dependsOn(n *yaml.Node, svc string) []Dependency {
	var out []Dependency
	switch n.Kind {
	case yaml.SequenceNode:
		for _, item := range n.Content {
			if s := p.scalar(resolve(item), svc+".depends_on"); s != "" {
				out = append(out, Dependency{Service: s, Condition: Started})
			}
		}
	case yaml.MappingNode:
		for _, kv := range pairs(n) {
			d := Dependency{Service: kv.key, Condition: Started}
			if !isNull(kv.value) {
				for _, opt := range pairs(kv.value) {
					switch opt.key {
					case "condition":
						d.Condition = p.scalar(opt.value, svc+".depends_on.condition")
						if d.Condition != Started && d.Condition != Healthy && d.Condition != Completed {
							p.errorf(opt.value, "%s : condition %q : service_started, service_healthy ou service_completed_successfully", svc, d.Condition)
						}
					case "required":
						if opt.value.Value != "true" {
							p.errorf(opt.value, "%s : les dépendances facultatives (required: false) ne sont pas prises en charge", svc)
						}
					default:
						p.errorf(opt.keyN, "%s.depends_on.%s n'est pas pris en charge", svc, opt.key)
					}
				}
			}
			out = append(out, d)
		}
	default:
		p.errorf(n, "%s.depends_on : une liste ou un objet est attendu", svc)
	}
	return out
}

func (p *parser) healthcheck(n *yaml.Node, svc string) *Healthcheck {
	if n.Kind != yaml.MappingNode {
		p.errorf(n, "%s.healthcheck : un objet est attendu", svc)
		return nil
	}
	h := &Healthcheck{}
	disabled := false
	duration := func(v *yaml.Node, what string) time.Duration {
		d, err := time.ParseDuration(p.scalar(v, what))
		if err != nil || d < 0 {
			p.errorf(v, "%s : une durée est attendue, par exemple 10s ou 1m30s", what)
		}
		return d
	}
	for _, kv := range pairs(n) {
		what := svc + ".healthcheck." + kv.key
		switch kv.key {
		case "test":
			if kv.value.Kind == yaml.ScalarNode {
				h.Test = []string{"CMD-SHELL", kv.value.Value}
				continue
			}
			h.Test = p.words(kv.value, what)
			if len(h.Test) > 0 && h.Test[0] == "NONE" {
				disabled = true
			} else if len(h.Test) < 2 || (h.Test[0] != "CMD" && h.Test[0] != "CMD-SHELL") {
				p.errorf(kv.value, "%s : [\"CMD\", …], [\"CMD-SHELL\", \"…\"] ou une commande", what)
			}
		case "interval":
			h.Interval = duration(kv.value, what)
		case "timeout":
			h.Timeout = duration(kv.value, what)
		case "start_period":
			h.StartPeriod = duration(kv.value, what)
		case "retries":
			r, err := strconv.Atoi(p.scalar(kv.value, what))
			if err != nil || r < 0 {
				p.errorf(kv.value, "%s : un nombre est attendu", what)
			}
			h.Retries = r
		case "disable":
			disabled = kv.value.Value == "true"
		default:
			p.errorf(kv.keyN, "%s n'est pas pris en charge", what)
		}
	}
	if disabled {
		return &Healthcheck{Test: []string{"NONE"}}
	}
	if len(h.Test) == 0 {
		p.errorf(n, "%s.healthcheck : test est obligatoire", svc)
		return nil
	}
	return h
}

func (p *parser) forgeyard(n *yaml.Node, svc *Service) {
	if n.Kind != yaml.MappingNode {
		p.errorf(n, "%s.x-forgeyard : un objet est attendu", svc.Name)
		return
	}
	for _, kv := range pairs(n) {
		what := svc.Name + ".x-forgeyard." + kv.key
		switch kv.key {
		case "expose":
			if kv.value.Kind != yaml.MappingNode {
				p.errorf(kv.value, "%s : { port: 80 } est attendu", what)
				continue
			}
			for _, opt := range pairs(kv.value) {
				if opt.key != "port" {
					p.errorf(opt.keyN, "%s.%s n'est pas pris en charge", what, opt.key)
					continue
				}
				port, err := strconv.Atoi(p.scalar(opt.value, what+".port"))
				if err != nil || port < 1 || port > 65535 {
					p.errorf(opt.value, "%s.port : le port sur lequel le service écoute, par exemple 80", what)
				}
				svc.Expose = port
			}
			if svc.Expose == 0 {
				p.errorf(kv.value, "%s : port est obligatoire", what)
			}
		case "backup":
			if kv.value.Kind != yaml.MappingNode {
				p.errorf(kv.value, "%s : { type: postgres } est attendu", what)
				continue
			}
			for _, opt := range pairs(kv.value) {
				if opt.key != "type" {
					p.errorf(opt.keyN, "%s.%s n'est pas pris en charge", what, opt.key)
					continue
				}
				svc.Backup = p.scalar(opt.value, what+".type")
				if svc.Backup != "postgres" {
					p.errorf(opt.value, "%s.type : seul postgres est pris en charge", what)
				}
			}
		case "memory":
			mb, err := strconv.Atoi(p.scalar(kv.value, what))
			if err != nil || mb < 64 || mb > 16384 {
				p.errorf(kv.value, "%s : entre 64 et 16384 (Mo)", what)
			}
			svc.MemoryMB = mb
		default:
			p.errorf(kv.keyN, "%s n'est pas pris en charge (expose, backup ou memory)", what)
		}
	}
}

// links checks the services together: dependencies, jobs, the exposed service, and orders them so that
// each one comes after what it depends on.
func (p *parser) links(st *Stack, services *yaml.Node) {
	line := map[string]*yaml.Node{}
	for _, kv := range pairs(services) {
		line[kv.key] = kv.keyN
	}
	byName := map[string]*Service{}
	for i := range st.Services {
		byName[st.Services[i].Name] = &st.Services[i]
	}
	exposed := []string{}
	for i := range st.Services {
		s := &st.Services[i]
		if s.Expose > 0 {
			exposed = append(exposed, s.Name)
		}
		for _, d := range s.DependsOn {
			dep, ok := byName[d.Service]
			switch {
			case d.Service == s.Name:
				p.errorf(line[s.Name], "%s ne peut pas dépendre de lui-même", s.Name)
			case !ok:
				if _, declared := line[d.Service]; !declared {
					p.errorf(line[s.Name], "%s dépend de %s, qui n'existe pas", s.Name, d.Service)
				}
			case d.Condition == Completed:
				dep.Job = true
			}
		}
	}
	if len(exposed) > 1 {
		p.errorf(line[exposed[1]], "un seul service exposé par projet : %s", strings.Join(exposed, ", "))
	}
	for _, s := range st.Services {
		if !s.Job {
			continue
		}
		if s.Expose > 0 {
			p.errorf(line[s.Name], "%s est un job (un service attend qu'il se termine) : il ne peut pas être exposé", s.Name)
		}
		if s.Restart == "always" || s.Restart == "unless-stopped" {
			p.errorf(line[s.Name], "%s est un job (un service attend qu'il se termine) : restart: %s le relancerait sans fin", s.Name, s.Restart)
		}
	}
	// Order what can be ordered even when the file is refused: the caller may still show the services
	// it could read, in their start order.
	ordered, cycle := Order(st.Services)
	if cycle != nil {
		p.errorf(line[cycle[0]], "dépendances en boucle : %s", strings.Join(append(cycle, cycle[0]), " → "))
		return
	}
	st.Services = ordered
}

// Order sorts services so that each comes after those it depends on, in name order otherwise. When they
// depend on each other in a loop, it returns the services of the loop instead.
func Order(services []Service) ([]Service, []string) {
	byName := map[string]Service{}
	names := []string{}
	for _, s := range services {
		byName[s.Name] = s
		names = append(names, s.Name)
	}
	sort.Strings(names)
	var out []Service
	state := map[string]int{} // 1 visiting, 2 done
	var stack []string
	var visit func(string) []string
	visit = func(name string) []string {
		switch state[name] {
		case 1:
			i := slices.Index(stack, name)
			return append([]string(nil), stack[i:]...)
		case 2:
			return nil
		}
		s, ok := byName[name]
		if !ok {
			return nil
		}
		state[name] = 1
		stack = append(stack, name)
		deps := make([]string, 0, len(s.DependsOn))
		for _, d := range s.DependsOn {
			deps = append(deps, d.Service)
		}
		sort.Strings(deps)
		for _, d := range deps {
			if cycle := visit(d); cycle != nil {
				return cycle
			}
		}
		stack = stack[:len(stack)-1]
		state[name] = 2
		out = append(out, s)
		return nil
	}
	for _, n := range names {
		if cycle := visit(n); cycle != nil {
			return nil, cycle
		}
	}
	return out, nil
}

// SplitWords splits a command line the way a shell would, without running one: spaces separate words,
// quotes keep them together, a backslash escapes the next character.
func SplitWords(s string) ([]string, error) {
	var out []string
	var cur strings.Builder
	inWord := false
	quote := rune(0)
	escaped := false
	for _, r := range s {
		switch {
		case escaped:
			cur.WriteRune(r)
			escaped = false
		case r == '\\' && quote != '\'':
			escaped, inWord = true, true
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote, inWord = r, true
		case r == ' ' || r == '\t' || r == '\n':
			if inWord {
				out = append(out, cur.String())
				cur.Reset()
				inWord = false
			}
		default:
			cur.WriteRune(r)
			inWord = true
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("guillemet %c non fermé", quote)
	}
	if inWord {
		out = append(out, cur.String())
	}
	return out, nil
}

var yamlLine = regexp.MustCompile(`line (\d+)`)

func yamlErrorLine(err error) int {
	if m := yamlLine.FindStringSubmatch(err.Error()); m != nil {
		n, _ := strconv.Atoi(m[1])
		return n
	}
	return 0
}

func yamlErrorText(err error) string {
	return strings.TrimPrefix(strings.TrimPrefix(err.Error(), "yaml: "), "unmarshal errors:\n  ")
}
