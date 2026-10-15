package docker

import "testing"

func TestCPULimit(t *testing.T) {
	cases := []struct {
		hc   HostConfig
		want float64
	}{
		{HostConfig{}, 0},
		{HostConfig{NanoCpus: 1_500_000_000}, 1.5},
		{HostConfig{CpuQuota: 200000}, 2},
		{HostConfig{CpuQuota: 50000, CpuPeriod: 100000}, 0.5},
		{HostConfig{CpusetCpus: "0-3,6"}, 5},
		{HostConfig{CpusetCpus: "2"}, 1},
		// --cpus wins over the rest, as Docker sets it.
		{HostConfig{NanoCpus: 2e9, CpusetCpus: "0-7"}, 2},
	}
	for _, c := range cases {
		if got := c.hc.CPULimit(); got != c.want {
			t.Errorf("%+v: %v, want %v", c.hc, got, c.want)
		}
	}
}
