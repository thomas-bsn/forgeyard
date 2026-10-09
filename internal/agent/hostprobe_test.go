package agent

import (
	"strings"
	"testing"
)

func TestParseDefaultGateway(t *testing.T) {
	route := `Iface	Destination	Gateway 	Flags	RefCnt	Use	Metric	Mask		MTU	Window	IRTT
eth0	0000A8C0	00000000	0001	0	0	0	00FFFFFF	0	0	0
eth0	00000000	0101A8C0	0003	0	0	0	00000000	0	0	0
`
	gw, err := parseDefaultGateway(strings.NewReader(route))
	if err != nil {
		t.Fatal(err)
	}
	if gw.String() != "192.168.1.1" {
		t.Fatalf("gateway = %s, want 192.168.1.1", gw)
	}
	if _, err := parseDefaultGateway(strings.NewReader("Iface\tDestination\tGateway\n")); err == nil {
		t.Fatal("want an error without a default route")
	}
}
