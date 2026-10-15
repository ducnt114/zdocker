package docker

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
)

func sample(total, system uint64, preTotal, preSystem uint64) container.StatsResponse {
	return container.StatsResponse{
		CPUStats:    container.CPUStats{CPUUsage: container.CPUUsage{TotalUsage: total}, SystemUsage: system, OnlineCPUs: 4},
		PreCPUStats: container.CPUStats{CPUUsage: container.CPUUsage{TotalUsage: preTotal}, SystemUsage: preSystem},
	}
}

func TestCPUPercent(t *testing.T) {
	tests := []struct {
		name string
		s    container.StatsResponse
		want float64
	}{
		// The container used 1/4 of the host's CPU time on a 4-CPU host:
		// one full CPU, 100%.
		{"one of four CPUs", sample(2_000, 8_000, 1_000, 4_000), 100},
		{"half a CPU", sample(1_500, 8_000, 1_000, 4_000), 50},
		{"first sample has no previous one", sample(1_000, 4_000, 0, 0), 0},
		{"no system delta", sample(2_000, 4_000, 1_000, 4_000), 0},
	}
	for _, tt := range tests {
		if got := newStats(tt.s).CPUPercent; math.Abs(got-tt.want) > 0.001 {
			t.Errorf("%s: %.2f%%, want %.2f%%", tt.name, got, tt.want)
		}
	}

	// Without online_cpus, the number of per-CPU counters is used.
	s := sample(2_000, 8_000, 1_000, 4_000)
	s.CPUStats.OnlineCPUs = 0
	s.CPUStats.CPUUsage.PercpuUsage = []uint64{1, 1}
	if got := newStats(s).CPUPercent; got != 50 {
		t.Errorf("percpu fallback: %.2f", got)
	}
}

func TestMemoryNetworkBlock(t *testing.T) {
	v2 := container.StatsResponse{
		MemoryStats: container.MemoryStats{Usage: 300 << 20, Limit: 1 << 30, Stats: map[string]uint64{"inactive_file": 100 << 20}},
		Networks:    map[string]container.NetworkStats{"eth0": {RxBytes: 10, TxBytes: 20}, "eth1": {RxBytes: 1, TxBytes: 2}},
		BlkioStats: container.BlkioStats{IoServiceBytesRecursive: []container.BlkioStatEntry{
			{Op: "read", Value: 4096}, {Op: "write", Value: 8192}, {Op: "Read", Value: 4}, {Op: "total", Value: 99999},
		}},
		PidsStats: container.PidsStats{Current: 7},
	}
	st := newStats(v2)
	if st.MemUsed != 200<<20 || math.Abs(st.MemPercent-19.53) > 0.01 {
		t.Errorf("memory (v2) = %d bytes, %.2f%%", st.MemUsed, st.MemPercent)
	}
	if st.NetRx != 11 || st.NetTx != 22 || st.BlockRead != 4100 || st.BlockWrite != 8192 || st.PIDs != 7 {
		t.Errorf("stats = %+v", st)
	}

	v1 := container.StatsResponse{MemoryStats: container.MemoryStats{Usage: 300, Limit: 1000,
		Stats: map[string]uint64{"total_inactive_file": 50, "inactive_file": 999}}}
	if got := newStats(v1).MemUsed; got != 250 {
		t.Errorf("memory (v1) = %d, want 250 (total_inactive_file wins)", got)
	}
}

func TestContainerStatsStream(t *testing.T) {
	d := newFakeDaemon(t)
	d.handle("GET /containers/c1/stats", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("stream") != "true" {
			t.Errorf("stream not requested: %s", r.URL.RawQuery)
		}
		enc := json.NewEncoder(w)
		for i := range 3 {
			enc.Encode(container.StatsResponse{Read: time.Unix(int64(1000+i), 0), PidsStats: container.PidsStats{Current: uint64(i)}})
			w.(http.Flusher).Flush()
		}
	})
	stats, errs, err := d.client().ContainerStats(context.Background(), "c1")
	if err != nil {
		t.Fatal(err)
	}
	var got []Stats
	for s := range stats {
		got = append(got, s)
	}
	if len(got) != 3 || got[2].PIDs != 2 || got[2].Time.Unix() != 1002 {
		t.Fatalf("got %+v", got)
	}
	select {
	case err := <-errs:
		t.Fatalf("unexpected error: %v", err)
	default:
	}
}
