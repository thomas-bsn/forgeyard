package agent

import (
	"bufio"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// listeningPorts are the TCP ports a container's processes listen on, read from the host's /proc for its
// main process (through FORGEYARD_HOST_ROOT when the agent runs in a container): it needs nothing in the
// container, not even a shell. Sockets bound to loopback are left out, as nothing outside can reach them.
func listeningPorts(pid int) []int32 {
	if pid <= 0 {
		return nil
	}
	root := os.Getenv("FORGEYARD_HOST_ROOT")
	if root == "" {
		root = "/"
	}
	var ports []int32
	for _, file := range []string{"tcp", "tcp6"} {
		f, err := os.Open(filepath.Join(root, "proc", strconv.Itoa(pid), "net", file))
		if err != nil {
			continue
		}
		ports = append(ports, parseListening(bufio.NewScanner(f))...)
		f.Close()
	}
	slices.Sort(ports)
	return slices.Compact(ports)
}

// parseListening reads a /proc/net/tcp(6) table: local address in hex, then the state, 0A for LISTEN.
func parseListening(sc *bufio.Scanner) []int32 {
	var ports []int32
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 4 || f[3] != "0A" {
			continue
		}
		addr, portHex, ok := strings.Cut(f[1], ":")
		if !ok || loopbackHex(addr) {
			continue
		}
		if p, err := strconv.ParseInt(portHex, 16, 32); err == nil && p > 0 {
			ports = append(ports, int32(p))
		}
	}
	return ports
}

// loopbackHex reports a loopback address as /proc writes it: 127.x.x.x in little-endian words, or ::1
// (and ::ffff:127.x.x.x).
func loopbackHex(addr string) bool {
	switch len(addr) {
	case 8:
		return strings.HasSuffix(addr, "7F")
	case 32:
		return addr == "00000000000000000000000001000000" ||
			(strings.HasPrefix(addr, "0000000000000000FFFF0000") && strings.HasSuffix(addr, "7F"))
	}
	return false
}
