package compose

import (
	"errors"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// BindMountChange is one source and destination approved for a Compose service.
type BindMountChange struct {
	Service  string
	Source   string
	Target   string
	ReadOnly bool
}

// MergeBindMounts keeps the current Compose configuration and replaces only its bind mounts with the
// requested mounts. Service membership must match the reviewed proposal and current project.
func MergeBindMounts(current, proposal string, requested []BindMountChange) (string, error) {
	var currentDoc, proposalDoc yaml.Node
	if err := yaml.Unmarshal([]byte(current), &currentDoc); err != nil {
		return "", err
	}
	if err := yaml.Unmarshal([]byte(proposal), &proposalDoc); err != nil {
		return "", err
	}
	servicesNode := func(doc *yaml.Node) (*yaml.Node, error) {
		if len(doc.Content) == 0 {
			return nil, errors.New("Compose vide")
		}
		for i := 0; i+1 < len(doc.Content[0].Content); i += 2 {
			if doc.Content[0].Content[i].Value == "services" {
				return doc.Content[0].Content[i+1], nil
			}
		}
		return nil, errors.New("services Compose manquants")
	}
	currentServices, err := servicesNode(&currentDoc)
	if err != nil {
		return "", err
	}
	proposalServices, err := servicesNode(&proposalDoc)
	if err != nil {
		return "", err
	}
	serviceMap := func(n *yaml.Node) map[string]*yaml.Node {
		out := map[string]*yaml.Node{}
		for i := 0; i+1 < len(n.Content); i += 2 {
			value := n.Content[i+1]
			if value.Kind == yaml.AliasNode {
				value = cloneYAMLNode(resolve(value))
				n.Content[i+1] = value
			}
			out[n.Content[i].Value] = value
		}
		return out
	}
	currentByName, proposalByName := serviceMap(currentServices), serviceMap(proposalServices)
	if len(currentByName) != len(proposalByName) {
		return "", errors.New("la liste des services a changé ; soumettez une nouvelle demande")
	}
	for name := range currentByName {
		if proposalByName[name] == nil {
			return "", errors.New("la liste des services a changé ; soumettez une nouvelle demande")
		}
	}
	requestedByService := map[string][]BindMountChange{}
	for _, mount := range requested {
		requestedByService[mount.Service] = append(requestedByService[mount.Service], mount)
	}
	for name, service := range currentByName {
		volumesNode := yamlField(service, "volumes")
		if volumesNode == nil {
			for _, field := range pairs(service) {
				if field.key == "volumes" {
					volumesNode = field.value
					break
				}
			}
		}
		if volumesNode == nil {
			volumesNode = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		} else {
			volumesNode = cloneYAMLNode(resolve(volumesNode))
		}
		if volumes := yamlField(service, "volumes"); volumes != nil {
			for i := 0; i+1 < len(service.Content); i += 2 {
				if service.Content[i].Value == "volumes" {
					service.Content[i+1] = volumesNode
					break
				}
			}
		} else {
			service.Content = append(service.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "volumes"}, volumesNode)
		}
		if volumesNode.Kind != yaml.SequenceNode {
			return "", errors.New("liste de volumes Compose invalide pour " + name)
		}
		// Keep named volumes from the current configuration, discarding bind mounts.
		kept := []*yaml.Node{}
		for _, item := range volumesNode.Content {
			resolved := item
			for resolved.Kind == yaml.AliasNode && resolved.Alias != nil {
				resolved = resolved.Alias
			}
			bind := resolved.Kind == yaml.ScalarNode && strings.HasPrefix(resolved.Value, "/")
			if resolved.Kind == yaml.MappingNode {
				for i := 0; i+1 < len(resolved.Content); i += 2 {
					if resolved.Content[i].Value == "type" && resolved.Content[i+1].Value == "bind" {
						bind = true
					}
				}
			}
			if !bind {
				kept = append(kept, item)
			}
		}
		for _, mount := range requestedByService[name] {
			volume := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: []*yaml.Node{
				{Kind: yaml.ScalarNode, Tag: "!!str", Value: "type"}, {Kind: yaml.ScalarNode, Tag: "!!str", Value: "bind"},
				{Kind: yaml.ScalarNode, Tag: "!!str", Value: "source"}, {Kind: yaml.ScalarNode, Tag: "!!str", Value: mount.Source},
				{Kind: yaml.ScalarNode, Tag: "!!str", Value: "target"}, {Kind: yaml.ScalarNode, Tag: "!!str", Value: mount.Target},
				{Kind: yaml.ScalarNode, Tag: "!!str", Value: "read_only"}, {Kind: yaml.ScalarNode, Tag: "!!bool", Value: strconv.FormatBool(mount.ReadOnly)},
			}}
			kept = append(kept, volume)
		}
		volumesNode.Content = kept
	}
	merged, err := yaml.Marshal(&currentDoc)
	return string(merged), err
}

func yamlField(n *yaml.Node, name string) *yaml.Node {
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == name {
			return n.Content[i+1]
		}
	}
	return nil
}

func cloneYAMLNode(n *yaml.Node) *yaml.Node {
	if n == nil {
		return nil
	}
	clone := *n
	clone.Anchor = ""
	if n.Kind == yaml.AliasNode {
		return &clone
	}
	clone.Content = make([]*yaml.Node, len(n.Content))
	for i, child := range n.Content {
		clone.Content[i] = cloneYAMLNode(child)
	}
	return &clone
}
