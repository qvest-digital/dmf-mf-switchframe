package main

import (
	"fmt"
	"log/slog"
	"os"
	"path"
	"runtime"
	"strconv"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/zsiec/switchframe/server/codec"
)

// maxAutoX264Threads is the slice thread count past which a 1080p50
// program encode stopped getting faster (BenchmarkX264ProgramThreads):
// x264 allows 17 slices at 1080p, and 24 threads measured no better than 16.
const maxAutoX264Threads = 16

// x264Threads is the program encoder's thread count and where it came from.
type x264Threads struct {
	N      int
	Source string // flag, env, cgroup, downward-api or host
	Cores  int    // the CPU cores the rule read; 0 for an override
}

// resolveX264Threads picks the program encoder's thread count:
// --x264-threads, else SWITCHFRAME_X264_THREADS, else two per core of the
// CPU limit, clamped to 2..maxAutoX264Threads. With the zerolatency tune
// every thread encodes a slice of the same frame, so more threads shorten
// one encode; a thread count above the limit only waits for the quota.
//
// The limit is the cgroup v2 CPU quota, else SWITCHFRAME_CPU_LIMIT (the
// chart sets it from resources.limits.cpu), else the CPUs the process may
// run on.
func resolveX264Threads(flagVal int, getenv func(string) string, readFile func(string) ([]byte, error), numCPU int) (x264Threads, error) {
	if flagVal < 0 {
		return x264Threads{}, fmt.Errorf("--x264-threads %d: want 0 (auto) or more", flagVal)
	}
	if flagVal > 0 {
		return x264Threads{N: flagVal, Source: "flag"}, nil
	}
	if v := getenv("SWITCHFRAME_X264_THREADS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return x264Threads{}, fmt.Errorf("SWITCHFRAME_X264_THREADS %q: want 0 (auto) or more", v)
		}
		if n > 0 {
			return x264Threads{N: n, Source: "env"}, nil
		}
	}

	cores, source, err := cpuLimit(getenv, readFile, numCPU)
	if err != nil {
		return x264Threads{}, err
	}
	return x264Threads{N: min(max(2*cores, 2), maxAutoX264Threads), Source: source, Cores: cores}, nil
}

// cpuLimit returns the CPU cores the process may use, rounded up, and
// where that figure came from.
func cpuLimit(getenv func(string) string, readFile func(string) ([]byte, error), numCPU int) (int, string, error) {
	if b, err := readFile(cgroupCPUMaxPath(readFile)); err == nil {
		cores, limited, err := cpuMaxCores(string(b))
		if err != nil {
			return 0, "", err
		}
		if limited {
			return cores, "cgroup", nil
		}
	}
	if v := getenv("SWITCHFRAME_CPU_LIMIT"); v != "" {
		cores, err := strconv.Atoi(v)
		if err != nil || cores <= 0 {
			return 0, "", fmt.Errorf("SWITCHFRAME_CPU_LIMIT %q: want whole cores, as the downward API gives them with divisor 1", v)
		}
		return cores, "downward-api", nil
	}
	return numCPU, "host", nil
}

// cgroupCPUMaxPath is the cpu.max of the process's own cgroup v2. In a
// container with its own cgroup namespace that is /sys/fs/cgroup/cpu.max.
func cgroupCPUMaxPath(readFile func(string) ([]byte, error)) string {
	cgPath := "/"
	if b, err := readFile("/proc/self/cgroup"); err == nil {
		for line := range strings.Lines(string(b)) {
			if p, ok := strings.CutPrefix(strings.TrimSpace(line), "0::"); ok {
				cgPath = p
			}
		}
	}
	return path.Join("/sys/fs/cgroup", cgPath, "cpu.max")
}

// cpuMaxCores parses a cgroup v2 cpu.max, "<quota> <period>" or
// "max <period>", into whole cores rounded up.
func cpuMaxCores(s string) (cores int, limited bool, err error) {
	f := strings.Fields(s)
	if len(f) != 2 {
		return 0, false, fmt.Errorf("cpu.max %q: want <quota> <period>", s)
	}
	period, err := strconv.ParseInt(f[1], 10, 64)
	if err != nil || period <= 0 {
		return 0, false, fmt.Errorf("cpu.max %q: bad period", s)
	}
	if f[0] == "max" {
		return 0, false, nil
	}
	quota, err := strconv.ParseInt(f[0], 10, 64)
	if err != nil || quota <= 0 {
		return 0, false, fmt.Errorf("cpu.max %q: bad quota", s)
	}
	return int((quota + period - 1) / period), true, nil
}

// x264ThreadsFromEnvironment resolves the count against this process.
func x264ThreadsFromEnvironment(flagVal int) (x264Threads, error) {
	return resolveX264Threads(flagVal, os.Getenv, os.ReadFile, runtime.NumCPU())
}

// programEncoderOpts is the program encoder's configuration: the
// --output-* flags and the x264 thread count.
func programEncoderOpts(cfg AppConfig) *codec.EncoderOptions {
	return &codec.EncoderOptions{
		Bitrate:  cfg.OutputBitrate,
		GOPSecs:  cfg.OutputGOPSecs,
		Preset:   codec.OutputPreset(cfg.OutputPreset),
		Timecode: cfg.OutputTimecode,
		Threads:  cfg.X264Threads.N,
	}
}

var descX264Threads = prometheus.NewDesc("switchframe_program_encoder_threads",
	"Threads, and so slices per frame, of the program's x264 encoder, by where the count came from.",
	[]string{"source"}, nil)

type x264ThreadsCollector struct{ t x264Threads }

func newX264ThreadsCollector(t x264Threads) prometheus.Collector { return x264ThreadsCollector{t} }

// Describe implements prometheus.Collector.
func (c x264ThreadsCollector) Describe(ch chan<- *prometheus.Desc) { ch <- descX264Threads }

// Collect implements prometheus.Collector.
func (c x264ThreadsCollector) Collect(ch chan<- prometheus.Metric) {
	ch <- prometheus.MustNewConstMetric(descX264Threads, prometheus.GaugeValue, float64(c.t.N), c.t.Source)
}

// logX264Threads reports the count and its source at startup.
func logX264Threads(t x264Threads) {
	args := []any{"threads", t.N, "source", t.Source}
	if t.Cores > 0 {
		args = append(args, "cores", t.Cores)
	}
	slog.Info("program x264 threads", args...)
}
