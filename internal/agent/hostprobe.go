package agent

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// hostProbeFile is written next to the join file, for the setup wizard to suggest how this machine
// receives web traffic.
const hostProbeFile = "host.json"

// hostProbe tells whether something on the host already answers on the web ports: "busy", "free" or
// "unknown" (e.g. a firewall between containers and the host).
type hostProbe struct {
	WebPorts string `json:"webPorts"`
}

// WriteHostProbe checks the host's ports 80 and 443 and writes the result next to joinFile.
func WriteHostProbe(ctx context.Context, joinFile string, logger *slog.Logger) {
	probe := hostProbe{WebPorts: probeWebPorts(ctx)}
	raw, _ := json.Marshal(probe)
	path := filepath.Join(filepath.Dir(joinFile), hostProbeFile)
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		logger.Warn("writing the host probe failed", "err", err)
		return
	}
	logger.Info("checked the host's web ports", "web_ports", probe.WebPorts)
}

// probeWebPorts connects to ports 80 and 443 of the host, reached through the container's default gateway.
// Both host programs and ports published by other containers answer there.
func probeWebPorts(ctx context.Context) string {
	gw, err := defaultGateway()
	if err != nil {
		return "unknown"
	}
	result := "free"
	for _, port := range []string{"80", "443"} {
		dctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		conn, err := (&net.Dialer{}).DialContext(dctx, "tcp", net.JoinHostPort(gw.String(), port))
		cancel()
		switch {
		case err == nil:
			conn.Close()
			return "busy"
		case !errors.Is(err, syscall.ECONNREFUSED):
			result = "unknown"
		}
	}
	return result
}

// defaultGateway reads the IPv4 default route of this network namespace.
func defaultGateway() (netip.Addr, error) {
	f, err := os.Open("/proc/net/route")
	if err != nil {
		return netip.Addr{}, err
	}
	defer f.Close()
	return parseDefaultGateway(f)
}

func parseDefaultGateway(r io.Reader) (netip.Addr, error) {
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		// Iface Destination Gateway Flags …, in little-endian hex.
		fields := strings.Fields(sc.Text())
		if len(fields) < 3 || fields[1] != "00000000" {
			continue
		}
		b, err := hex.DecodeString(fields[2])
		if err != nil || len(b) != 4 {
			continue
		}
		var ip [4]byte
		binary.BigEndian.PutUint32(ip[:], binary.LittleEndian.Uint32(b))
		if addr := netip.AddrFrom4(ip); !addr.IsUnspecified() {
			return addr, nil
		}
	}
	return netip.Addr{}, errors.New("no default route")
}
