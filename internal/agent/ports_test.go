package agent

import (
	"bufio"
	"slices"
	"strings"
	"testing"
)

func TestParseListening(t *testing.T) {
	table := `  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 00000000:0BB8 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 1 1
   1: 0100007F:1F90 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 2 1
   2: 0200A8C0:0050 0300A8C0:D2F0 01 00000000:00000000 00:00000000 00000000     0        0 3 1
   3: 00000000000000000000000000000000:2382 00000000000000000000000000000000:0000 0A 0 0 0 0 0 4 1
   4: 00000000000000000000000001000000:1F91 00000000000000000000000000000000:0000 0A 0 0 0 0 0 5 1
   5: 0000000000000000FFFF00000100007F:1F92 00000000000000000000000000000000:0000 0A 0 0 0 0 0 6 1
`
	got := parseListening(bufio.NewScanner(strings.NewReader(table)))
	// 3000 and 9090 listen everywhere; 8080-8082 only on loopback; 80 is an established connection.
	if !slices.Equal(got, []int32{3000, 9090}) {
		t.Fatalf("ports: %v", got)
	}
}
