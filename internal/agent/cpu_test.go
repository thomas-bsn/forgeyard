package agent

import (
	"math"
	"testing"
)

func TestCPUPercentIsInPercentOfOneCore(t *testing.T) {
	// 3 s on a 12-core machine: 36 s of CPU time in all; the container used 27.264 s of it.
	prev := cpuSample{total: 0, system: 0}
	cur := cpuSample{total: 27_264_000_000, system: 36_000_000_000}
	got := cpuPercent(cur, prev, 12)
	if math.Abs(got-908.8) > 0.01 {
		t.Fatalf("got %.2f %%, want 908.8 %% (9.09 cores of 12)", got)
	}
	// A whole machine busy is 100 % per core, never capped.
	if got := cpuPercent(cpuSample{total: 36e9, system: 36e9}, prev, 12); got != 1200 {
		t.Fatalf("full machine: %.1f", got)
	}
}
