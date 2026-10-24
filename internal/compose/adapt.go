package compose

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// PortCandidate is a Compose port mapping that can be reviewed for Forgeyard's single HTTPS route.
type PortCandidate struct {
	Service   string `json:"service"`
	Published int    `json:"published,omitempty"`
	Target    int    `json:"target"`
	Protocol  string `json:"protocol"`
}

// AnalyzePorts finds numeric TCP/UDP ports without interpreting which service should be public.
func AnalyzePorts(src []byte) ([]PortCandidate, error) {
	root, err := composeRoot(src)
	if err != nil {
		return nil, err
	}
	services := mappingValue(root, "services")
	if services == nil || services.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("services: doit être un objet")
	}
	var out []PortCandidate
	for i := 0; i+1 < len(services.Content); i += 2 {
		name, service := services.Content[i].Value, resolve(services.Content[i+1])
		if service == nil || service.Kind != yaml.MappingNode {
			continue
		}
		var ports *yaml.Node
		for _, field := range pairs(service) {
			if field.key == "ports" {
				ports = field.value
				break
			}
		}
		if ports == nil {
			continue
		}
		if mappingValue(service, "ports") == nil {
			return nil, fmt.Errorf("service %s : ports provient d'une ancre YAML et doit être adapté manuellement", name)
		}
		if ports.Kind != yaml.SequenceNode {
			return nil, fmt.Errorf("service %s : ports doit être une liste pour être adapté", name)
		}
		for _, item := range ports.Content {
			candidate, ok := portCandidate(name, resolve(item))
			if !ok {
				return nil, fmt.Errorf("service %s : format de ports non reconnu ; adapte cette déclaration manuellement", name)
			}
			out = append(out, candidate)
		}
	}
	return out, nil
}

// AdaptPorts removes host port publication and, when chosen, adds Forgeyard's HTTPS exposure to one service.
// Other services remain reachable to their peers on the project's private network.
func AdaptPorts(src []byte, exposeService string, exposePort int) (string, []PortCandidate, error) {
	root, err := composeRoot(src)
	if err != nil {
		return "", nil, err
	}
	services := mappingValue(root, "services")
	if services == nil || services.Kind != yaml.MappingNode {
		return "", nil, fmt.Errorf("services: doit être un objet")
	}
	candidates, err := AnalyzePorts(src)
	if err != nil {
		return "", nil, err
	}
	selected := false
	for _, c := range candidates {
		if c.Service == exposeService && c.Target == exposePort && c.Protocol == "tcp" {
			selected = true
		}
	}
	if exposeService != "" && !selected {
		return "", candidates, fmt.Errorf("le port choisi n'est pas un port TCP du Compose")
	}
	for i := 0; i+1 < len(services.Content); i += 2 {
		name, service := services.Content[i].Value, resolve(services.Content[i+1])
		if service.Kind != yaml.MappingNode {
			continue
		}
		kept := make([]*yaml.Node, 0, len(service.Content))
		for j := 0; j+1 < len(service.Content); j += 2 {
			if service.Content[j].Value == "ports" {
				continue
			}
			kept = append(kept, service.Content[j], service.Content[j+1])
		}
		service.Content = kept
		if name == exposeService {
			if err := setExpose(service, exposePort); err != nil {
				return "", candidates, err
			}
		}
	}
	var out bytes.Buffer
	enc := yaml.NewEncoder(&out)
	enc.SetIndent(2)
	if err := enc.Encode(root); err != nil {
		return "", candidates, err
	}
	_ = enc.Close()
	return out.String(), candidates, nil
}

func composeRoot(src []byte) (*yaml.Node, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(src, &doc); err != nil {
		return nil, err
	}
	if len(doc.Content) == 0 || resolve(doc.Content[0]).Kind != yaml.MappingNode {
		return nil, fmt.Errorf("le fichier doit contenir un objet Compose")
	}
	return resolve(doc.Content[0]), nil
}

func mappingValue(n *yaml.Node, key string) *yaml.Node {
	n = resolve(n)
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return resolve(n.Content[i+1])
		}
	}
	return nil
}

func portCandidate(service string, item *yaml.Node) (PortCandidate, bool) {
	c := PortCandidate{Service: service, Protocol: "tcp"}
	if item.Kind == yaml.ScalarNode {
		parts := strings.Split(item.Value, "/")
		protocol := "tcp"
		if len(parts) == 2 {
			protocol = parts[1]
		}
		c.Protocol = protocol
		ports := strings.Split(parts[0], ":")
		target, err := strconv.Atoi(ports[len(ports)-1])
		if err != nil || target < 1 || target > 65535 {
			return c, false
		}
		c.Target = target
		if len(ports) > 1 {
			if published, err := strconv.Atoi(ports[len(ports)-2]); err == nil {
				c.Published = published
			}
		}
		return c, true
	}
	if item.Kind != yaml.MappingNode {
		return c, false
	}
	for i := 0; i+1 < len(item.Content); i += 2 {
		key, value := item.Content[i].Value, resolve(item.Content[i+1]).Value
		switch key {
		case "target":
			c.Target, _ = strconv.Atoi(value)
		case "published":
			c.Published, _ = strconv.Atoi(value)
		case "protocol":
			c.Protocol = value
		}
	}
	return c, c.Target > 0 && c.Target <= 65535 && (c.Protocol == "tcp" || c.Protocol == "udp")
}

func setExpose(service *yaml.Node, port int) error {
	x := mappingValue(service, "x-forgeyard")
	if x == nil {
		x = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		service.Content = append(service.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "x-forgeyard"}, x)
	}
	if x.Kind != yaml.MappingNode {
		return fmt.Errorf("x-forgeyard du service doit être un objet pour ajouter expose")
	}
	if mappingValue(x, "expose") != nil {
		return fmt.Errorf("le service choisi a déjà une configuration x-forgeyard.expose")
	}
	expose := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: []*yaml.Node{
		{Kind: yaml.ScalarNode, Tag: "!!str", Value: "port"},
		{Kind: yaml.ScalarNode, Tag: "!!int", Value: strconv.Itoa(port)},
	}}
	x.Content = append(x.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "expose"}, expose)
	return nil
}
