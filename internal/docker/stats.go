package docker

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/moby/moby/api/types/container"
	mobyclient "github.com/moby/moby/client"
)

// Stats is one sample of a container's resource usage, computed the way
// `docker stats` does.
type Stats struct {
	Time       time.Time
	CPUPercent float64 // 100 = one full CPU; can exceed 100 with several CPUs
	MemUsed    uint64  // without the page cache
	MemLimit   uint64
	MemPercent float64
	NetRx      uint64
	NetTx      uint64
	BlockRead  uint64
	BlockWrite uint64
	PIDs       uint64
}

// ContainerStats streams one sample per second until ctx is cancelled or
// the container stops. The first channel is closed at the end; an error
// other than cancellation is sent on the second channel first.
func (c *client) ContainerStats(ctx context.Context, id string) (<-chan Stats, <-chan error, error) {
	res, err := c.api.ContainerStats(ctx, id, mobyclient.ContainerStatsOptions{Stream: true})
	if err != nil {
		return nil, nil, err
	}
	out := make(chan Stats, 4)
	errs := make(chan error, 1)
	go func() {
		defer close(out)
		defer res.Body.Close()
		dec := json.NewDecoder(res.Body) // a stream of JSON objects
		for {
			var raw container.StatsResponse
			if err := dec.Decode(&raw); err != nil {
				if !errors.Is(err, io.EOF) && ctx.Err() == nil {
					errs <- err
				}
				return
			}
			select {
			case out <- newStats(raw):
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, errs, nil
}

// newStats derives the numbers `docker stats` shows from a raw sample.
func newStats(s container.StatsResponse) Stats {
	st := Stats{
		Time:       s.Read,
		CPUPercent: cpuPercent(s.CPUStats, s.PreCPUStats),
		MemLimit:   s.MemoryStats.Limit,
		PIDs:       s.PidsStats.Current,
	}

	// Memory: the page cache can be reclaimed, so like the CLI subtract
	// it (cgroup v2 calls it inactive_file, v1 total_inactive_file).
	used := s.MemoryStats.Usage
	cache := s.MemoryStats.Stats["inactive_file"]
	if v, ok := s.MemoryStats.Stats["total_inactive_file"]; ok {
		cache = v
	}
	if cache < used {
		used -= cache
	}
	st.MemUsed = used
	if st.MemLimit > 0 {
		st.MemPercent = float64(used) / float64(st.MemLimit) * 100
	}

	for _, n := range s.Networks {
		st.NetRx += n.RxBytes
		st.NetTx += n.TxBytes
	}
	for _, e := range s.BlkioStats.IoServiceBytesRecursive {
		switch e.Op {
		case "read", "Read":
			st.BlockRead += e.Value
		case "write", "Write":
			st.BlockWrite += e.Value
		}
	}
	return st
}

// cpuPercent compares this sample with the previous one the daemon sent
// along (precpu_stats): the container's share of the host's CPU time over
// that interval, times the number of CPUs.
func cpuPercent(cur, pre container.CPUStats) float64 {
	if pre.SystemUsage == 0 {
		// First sample: no previous reading to compare with. The system
		// counter runs since boot, so a delta against zero is meaningless.
		return 0
	}
	cpuDelta := float64(cur.CPUUsage.TotalUsage) - float64(pre.CPUUsage.TotalUsage)
	sysDelta := float64(cur.SystemUsage) - float64(pre.SystemUsage)
	cpus := float64(cur.OnlineCPUs)
	if cpus == 0 {
		cpus = float64(len(cur.CPUUsage.PercpuUsage))
	}
	if cpuDelta <= 0 || sysDelta <= 0 || cpus == 0 {
		return 0 // idle, or counters went backwards (restart)
	}
	return cpuDelta / sysDelta * cpus * 100
}
