package compose

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// SetServiceMemory gives a service of a compose file another memory limit. Only the value changes: the
// rest of the file, comments and layout included, stays as it was written. A limit already set is changed
// where it is (x-forgeyard.memory, mem_limit or deploy.resources.limits.memory, each in its own unit);
// without one, a mem_limit is added, which docker compose reads too.
//
// It refuses rather than guesses when the limit is not the service's own to change: set by a variable, or
// shared with other services through a YAML anchor.
func SetServiceMemory(src []byte, service string, mb int) ([]byte, error) {
	if mb < MinMemoryMB || mb > MaxMemoryMB {
		return nil, fmt.Errorf("mémoire : entre %d et %d Mo", MinMemoryMB, MaxMemoryMB)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(src, &doc); err != nil || len(doc.Content) == 0 {
		return nil, errors.New("le docker-compose.yml du projet ne se lit pas : corrigez-le d'abord")
	}
	svc, err := ownService(doc.Content[0], service)
	if err != nil {
		return nil, err
	}

	type target struct {
		node *yaml.Node
		text string
	}
	var targets []target
	if v := own(svc, "mem_limit"); v != nil {
		targets = append(targets, target{v, strconv.Itoa(mb) + "m"})
	}
	if v := own(own(svc, "x-forgeyard"), "memory"); v != nil {
		targets = append(targets, target{v, strconv.Itoa(mb)})
	}
	if v := own(own(own(own(svc, "deploy"), "resources"), "limits"), "memory"); v != nil {
		targets = append(targets, target{v, strconv.Itoa(mb) + "M"})
	}

	lines := strings.SplitAfter(string(src), "\n")
	if len(targets) == 0 {
		// A limit the service only inherits would be shared: changing it here would change the others.
		for _, kv := range pairs(svc) {
			if kv.key == "mem_limit" || kv.key == "deploy" || (kv.key == "x-forgeyard" && own(resolve(kv.value), "memory") != nil) {
				return nil, errors.New("la mémoire de ce service vient d'une ancre YAML partagée : modifiez-la dans le docker-compose.yml")
			}
		}
		if lines, err = insertKey(lines, svc, "mem_limit: "+strconv.Itoa(mb)+"m"); err != nil {
			return nil, err
		}
	}
	for _, t := range targets {
		if lines, err = replaceScalar(lines, t.node, t.text); err != nil {
			return nil, err
		}
	}
	out := []byte(strings.Join(lines, ""))

	// The edited file must still read, and give the service the limit asked for, and only one.
	var check yaml.Node
	if err := yaml.Unmarshal(out, &check); err != nil || len(check.Content) == 0 {
		return nil, errors.New("modification impossible sans casser le fichier : modifiez le docker-compose.yml")
	}
	if got, err := serviceMemoryMB(check.Content[0], service); err != nil || got != mb {
		return nil, errors.New("modification impossible sans casser le fichier : modifiez le docker-compose.yml")
	}
	return out, nil
}

// own returns the value of a mapping's own key (not one merged from an anchor), unresolved; nil if absent.
func own(n *yaml.Node, key string) *yaml.Node {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if k := n.Content[i]; k.Value == key && k.Tag != "!!merge" {
			return n.Content[i+1]
		}
	}
	return nil
}

func ownService(root *yaml.Node, service string) (*yaml.Node, error) {
	services := own(root, "services")
	if services == nil || services.Kind != yaml.MappingNode {
		return nil, errors.New("le docker-compose.yml du projet n'a pas de services:")
	}
	svc := own(services, service)
	switch {
	case svc == nil:
		return nil, fmt.Errorf("le service %s n'est pas dans le docker-compose.yml du projet", service)
	case svc.Kind == yaml.AliasNode:
		return nil, errors.New("ce service est une ancre YAML partagée : modifiez sa mémoire dans le docker-compose.yml")
	case svc.Kind != yaml.MappingNode:
		return nil, fmt.Errorf("le service %s n'est pas un objet", service)
	}
	return svc, nil
}

// serviceMemoryMB reads back the limit a service ends up with, as Parse does, without its variables.
func serviceMemoryMB(root *yaml.Node, service string) (int, error) {
	svc, err := ownService(root, service)
	if err != nil {
		return 0, err
	}
	p := &parser{}
	mem := memorySources{service: service}
	for _, kv := range pairs(svc) {
		switch kv.key {
		case "mem_limit":
			if mb, ok := p.memorySize(kv.value, "mem_limit"); ok {
				mem.add(p, kv.value, "mem_limit", mb)
			}
		case "deploy":
			p.deploy(kv.value, service, &mem)
		case "x-forgeyard":
			if v := own(resolve(kv.value), "memory"); v != nil {
				mb, err := strconv.Atoi(resolve(v).Value)
				if err != nil {
					return 0, err
				}
				mem.add(p, v, "x-forgeyard.memory", mb)
			}
		}
	}
	if len(p.errs) > 0 {
		return 0, p.errs
	}
	return mem.mb, nil
}

// replaceScalar writes text in place of a one-line scalar, keeping its quotes.
func replaceScalar(lines []string, n *yaml.Node, text string) ([]string, error) {
	if n.Kind != yaml.ScalarNode {
		return nil, errors.New("la mémoire de ce service vient d'une ancre YAML : modifiez-la dans le docker-compose.yml")
	}
	if strings.Contains(n.Value, "$") {
		return nil, errors.New("la mémoire de ce service vient d'une variable : changez la variable du projet")
	}
	raw := n.Value
	switch n.Style {
	case 0:
	case yaml.SingleQuotedStyle:
		raw, text = "'"+raw+"'", "'"+text+"'"
	case yaml.DoubleQuotedStyle:
		raw, text = `"`+raw+`"`, `"`+text+`"`
	default:
		return nil, errors.New("la mémoire de ce service est écrite sur plusieurs lignes : modifiez-la dans le docker-compose.yml")
	}
	if n.Line < 1 || n.Line > len(lines) {
		return nil, errors.New("modification impossible : modifiez le docker-compose.yml")
	}
	line := []rune(lines[n.Line-1]) // yaml.v3 counts columns in characters
	at := n.Column - 1
	if at < 0 || at+len([]rune(raw)) > len(line) || string(line[at:at+len([]rune(raw))]) != raw {
		return nil, errors.New("modification impossible : modifiez le docker-compose.yml")
	}
	lines[n.Line-1] = string(line[:at]) + text + string(line[at+len([]rune(raw)):])
	return lines, nil
}

// insertKey adds a key to a block mapping, as its first key, at the indentation of the others.
func insertKey(lines []string, m *yaml.Node, entry string) ([]string, error) {
	if m.Style&yaml.FlowStyle != 0 || len(m.Content) == 0 {
		return nil, errors.New("ce service est écrit sur une ligne : ajoutez mem_limit dans le docker-compose.yml")
	}
	first := m.Content[0]
	if first.Line < 1 || first.Line > len(lines) {
		return nil, errors.New("modification impossible : modifiez le docker-compose.yml")
	}
	indent := first.Column - 1
	line := lines[first.Line-1]
	if indent < 1 || len(line) < indent || strings.TrimLeft(line[:indent], " ") != "" {
		return nil, errors.New("modification impossible : modifiez le docker-compose.yml")
	}
	added := strings.Repeat(" ", indent) + entry + "\n"
	return append(lines[:first.Line-1], append([]string{added}, lines[first.Line-1:]...)...), nil
}
