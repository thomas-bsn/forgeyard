package compose

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// interpolate replaces the variables in every value of the file (keys are left as they are), as docker
// compose does: ${VAR}, $VAR, ${VAR:-default} (also when empty), ${VAR-default} (only when unset),
// ${VAR:?message} and ${VAR?message} (an error), ${VAR:+other} and ${VAR+other}; $$ is a dollar sign.
func (p *parser) interpolate(n *yaml.Node) {
	switch n.Kind {
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			p.interpolate(n.Content[i+1])
		}
	case yaml.SequenceNode, yaml.DocumentNode:
		for _, c := range n.Content {
			p.interpolate(c)
		}
	case yaml.ScalarNode:
		if !strings.Contains(n.Value, "$") {
			return
		}
		v, err := Interpolate(n.Value, p.vars)
		if err != nil {
			p.errorf(n, "%v", err)
			return
		}
		if v != n.Value && n.Tag != "!!str" {
			n.Tag = "!!str" // "${PORT}" stays a string to read, whatever it holds
		}
		n.Value = v
	}
}

// Interpolate replaces the variables of one value. A variable that is not set, without a default, is an
// error: the stack must define it.
func Interpolate(s string, vars map[string]string) (string, error) {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '$' {
			b.WriteByte(c)
			continue
		}
		if i+1 >= len(s) {
			b.WriteByte('$')
			continue
		}
		next := s[i+1]
		switch {
		case next == '$':
			b.WriteByte('$')
			i++
		case next == '{':
			end := closingBrace(s, i+2)
			if end < 0 {
				return "", fmt.Errorf("${ non fermé dans %q", s)
			}
			v, err := expand(s[i+2:end], vars)
			if err != nil {
				return "", err
			}
			b.WriteString(v)
			i = end
		case isNameStart(next):
			j := i + 1
			for j < len(s) && isNameChar(s[j]) {
				j++
			}
			name := s[i+1 : j]
			v, ok := vars[name]
			if !ok {
				return "", fmt.Errorf("variable %s non définie : ajoutez-la aux variables du projet", name)
			}
			b.WriteString(v)
			i = j - 1
		default:
			b.WriteByte('$')
		}
	}
	return b.String(), nil
}

// closingBrace finds the } closing a ${, allowing nested ${…} in a default value.
func closingBrace(s string, from int) int {
	depth := 1
	for i := from; i < len(s); i++ {
		switch {
		case s[i] == '$' && i+1 < len(s) && s[i+1] == '{':
			depth++
			i++
		case s[i] == '}':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

func expand(expr string, vars map[string]string) (string, error) {
	j := 0
	for j < len(expr) && isNameChar(expr[j]) {
		j++
	}
	name, rest := expr[:j], expr[j:]
	if name == "" || !isNameStart(name[0]) {
		return "", fmt.Errorf("${%s} : nom de variable invalide", expr)
	}
	v, set := vars[name]
	if rest == "" {
		if !set {
			return "", fmt.Errorf("variable %s non définie : ajoutez-la aux variables du projet ou donnez une valeur par défaut (${%s:-…})", name, name)
		}
		return v, nil
	}
	colon := strings.HasPrefix(rest, ":")
	op := strings.TrimPrefix(rest, ":")
	if op == "" {
		return "", fmt.Errorf("${%s} : opérateur manquant", expr)
	}
	word, err := Interpolate(op[1:], vars)
	if err != nil {
		return "", err
	}
	empty := !set || (colon && v == "")
	switch op[0] {
	case '-':
		if empty {
			return word, nil
		}
		return v, nil
	case '?':
		if empty {
			if word == "" {
				word = "obligatoire"
			}
			return "", fmt.Errorf("variable %s : %s", name, word)
		}
		return v, nil
	case '+':
		if empty {
			return "", nil
		}
		return word, nil
	}
	return "", fmt.Errorf("${%s} : opérateur %q non pris en charge", expr, rest)
}

func isNameStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isNameChar(c byte) bool {
	return isNameStart(c) || (c >= '0' && c <= '9')
}
