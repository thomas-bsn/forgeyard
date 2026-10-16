package nodes

import (
	"testing"
	"time"

	"github.com/thomas-bsn/forgeyard/internal/agentpb"
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

func TestGpuSamplesAverageTheirWindow(t *testing.T) {
	start := time.Unix(1_000_000, 0)
	var s []GpuSample
	for i, util := range []float64{20, 100, 60} {
		s = addGpuSample(s, start.Add(time.Duration(i*5)*time.Second), &agentpb.GpuMetric{Id: "GPU-a", UtilizationPercent: util, MemoryUsedBytes: 1000, MemoryTotalBytes: 4000})
	}
	if len(s) != 1 || s[0].UtilizationPercent != 60 || s[0].MemoryUsedBytes != 1000 || s[0].MemoryTotalBytes != 4000 {
		t.Fatalf("%+v", s)
	}
}
