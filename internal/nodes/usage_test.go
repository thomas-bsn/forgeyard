package nodes

import (
	"testing"
	"time"
)

func TestUsageSamplesAverageTheirWindow(t *testing.T) {
	start := time.Unix(1_000_000, 0)
	var s []UsageSample
	// Five reports 3 s apart fall in one 15 s point: a 900 % peak between two points still counts.
	for i, cpu := range []float64{100, 100, 900, 100, 100} {
		s = addSample(s, start.Add(time.Duration(i*3)*time.Second), cpu, uint64(1000*(i+1)))
	}
	if len(s) != 1 || s[0].CPUPercent != 260 || s[0].MemoryUsedBytes != 3000 || s[0].At != start.Unix() {
		t.Fatalf("one averaged point expected: %+v", s)
	}
	s = addSample(s, start.Add(15*time.Second), 50, 10)
	if len(s) != 2 || s[1].CPUPercent != 50 {
		t.Fatalf("a new point every 15 s: %+v", s)
	}
}
