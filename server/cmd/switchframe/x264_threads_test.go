package main

import (
	"errors"
	"io/fs"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/codec"
	"github.com/zsiec/switchframe/server/internal/metricstest"
)

func TestCPUMaxCores(t *testing.T) {
	for _, tc := range []struct {
		in      string
		cores   int
		limited bool
	}{
		{"2800000 100000\n", 28, true},
		{"200000 100000", 2, true},
		// A fractional quota rounds up, as the downward API does.
		{"150000 100000", 2, true},
		{"50000 100000", 1, true},
		{"max 100000\n", 0, false},
	} {
		cores, limited, err := cpuMaxCores(tc.in)
		require.NoError(t, err, tc.in)
		require.Equal(t, tc.cores, cores, tc.in)
		require.Equal(t, tc.limited, limited, tc.in)
	}
	for _, in := range []string{"", "max", "abc 100000", "100000 0", "-1 100000", "1 2 3"} {
		_, _, err := cpuMaxCores(in)
		require.Error(t, err, "%q", in)
	}
}

// host is the files and environment one resolveX264Threads call sees.
type host struct {
	files map[string]string
	env   map[string]string
	ncpu  int
}

func (h host) resolve(flagVal int) (x264Threads, error) {
	return resolveX264Threads(flagVal,
		func(k string) string { return h.env[k] },
		func(p string) ([]byte, error) {
			if s, ok := h.files[p]; ok {
				return []byte(s), nil
			}
			return nil, fs.ErrNotExist
		},
		h.ncpu)
}

// inPod is a container in its own cgroup namespace with the given cpu.max.
func inPod(cpuMax string) map[string]string {
	return map[string]string{
		"/proc/self/cgroup":      "0::/\n",
		"/sys/fs/cgroup/cpu.max": cpuMax,
	}
}

func TestResolveX264Threads_Auto(t *testing.T) {
	for name, tc := range map[string]struct {
		h    host
		want x264Threads
	}{
		// The rule: two slice threads per core of the limit, at least 2,
		// at most the 16 past which BenchmarkX264ProgramThreads stopped
		// getting faster.
		"limit 28 caps at 16": {host{files: inPod("2800000 100000"), ncpu: 128}, x264Threads{N: 16, Source: "cgroup", Cores: 28}},
		"limit 8":             {host{files: inPod("800000 100000"), ncpu: 128}, x264Threads{N: 16, Source: "cgroup", Cores: 8}},
		"limit 4":             {host{files: inPod("400000 100000"), ncpu: 128}, x264Threads{N: 8, Source: "cgroup", Cores: 4}},
		"limit 500m":          {host{files: inPod("50000 100000"), ncpu: 128}, x264Threads{N: 2, Source: "cgroup", Cores: 1}},
		// The cgroup quota wins over the chart's value: it is what the
		// kernel enforces.
		"cgroup before downward API": {
			host{files: inPod("400000 100000"), env: map[string]string{"SWITCHFRAME_CPU_LIMIT": "28"}, ncpu: 128},
			x264Threads{N: 8, Source: "cgroup", Cores: 4},
		},
		"no quota, downward API": {
			host{files: inPod("max 100000"), env: map[string]string{"SWITCHFRAME_CPU_LIMIT": "3"}, ncpu: 128},
			x264Threads{N: 6, Source: "downward-api", Cores: 3},
		},
		"no cgroup v2, downward API": {
			host{env: map[string]string{"SWITCHFRAME_CPU_LIMIT": "2"}, ncpu: 128},
			x264Threads{N: 4, Source: "downward-api", Cores: 2},
		},
		"nothing, host CPUs":  {host{ncpu: 128}, x264Threads{N: 16, Source: "host", Cores: 128}},
		"one host CPU":        {host{ncpu: 1}, x264Threads{N: 2, Source: "host", Cores: 1}},
		"no quota, host CPUs": {host{files: inPod("max 100000"), ncpu: 3}, x264Threads{N: 6, Source: "host", Cores: 3}},
		// Outside a cgroup namespace the process's own cgroup holds the
		// quota.
		"nested cgroup": {
			host{files: map[string]string{
				"/proc/self/cgroup":                       "0::/kubepods/pod1/c1\n",
				"/sys/fs/cgroup/kubepods/pod1/c1/cpu.max": "300000 100000",
				"/sys/fs/cgroup/cpu.max":                  "max 100000",
			}, ncpu: 128},
			x264Threads{N: 6, Source: "cgroup", Cores: 3},
		},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := tc.h.resolve(0)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestResolveX264Threads_Override(t *testing.T) {
	h := host{files: inPod("2800000 100000"), env: map[string]string{"SWITCHFRAME_X264_THREADS": "6"}, ncpu: 128}

	got, err := h.resolve(0)
	require.NoError(t, err)
	require.Equal(t, x264Threads{N: 6, Source: "env"}, got, "the env var overrides the rule")

	got, err = h.resolve(12)
	require.NoError(t, err)
	require.Equal(t, x264Threads{N: 12, Source: "flag"}, got, "the flag overrides the env var")

	// An override is not clamped: it is the operator's count.
	got, err = host{ncpu: 4}.resolve(24)
	require.NoError(t, err)
	require.Equal(t, x264Threads{N: 24, Source: "flag"}, got)

	h.env["SWITCHFRAME_X264_THREADS"] = "0"
	got, err = h.resolve(0)
	require.NoError(t, err)
	require.Equal(t, x264Threads{N: 16, Source: "cgroup", Cores: 28}, got, "0 means auto")
}

func TestResolveX264Threads_RejectsBadValues(t *testing.T) {
	_, err := host{ncpu: 8}.resolve(-1)
	require.Error(t, err, "negative flag")

	for _, v := range []string{"-3", "x", "1.5"} {
		_, err := host{env: map[string]string{"SWITCHFRAME_X264_THREADS": v}, ncpu: 8}.resolve(0)
		require.Error(t, err, "SWITCHFRAME_X264_THREADS=%q", v)
	}
	for _, v := range []string{"0", "-2", "x", "500m"} {
		_, err := host{env: map[string]string{"SWITCHFRAME_CPU_LIMIT": v}, ncpu: 8}.resolve(0)
		require.Error(t, err, "SWITCHFRAME_CPU_LIMIT=%q", v)
	}
	_, err = host{files: inPod("garbage"), ncpu: 8}.resolve(0)
	require.Error(t, err, "malformed cpu.max")
}

func TestResolveX264Threads_UnreadableCgroupFallsThrough(t *testing.T) {
	h := host{ncpu: 4}
	got, err := resolveX264Threads(0, func(string) string { return "" },
		func(string) ([]byte, error) { return nil, errors.New("permission denied") }, h.ncpu)
	require.NoError(t, err)
	require.Equal(t, x264Threads{N: 8, Source: "host", Cores: 4}, got)
}

// The program encoder is built from programEncoderOpts, so the resolved
// count reaches it whatever --output-* flags are set.
func TestProgramEncoderOptsCarriesX264Threads(t *testing.T) {
	opts := programEncoderOpts(AppConfig{X264Threads: x264Threads{N: 12, Source: "cgroup"}})
	require.NotNil(t, opts)
	require.Equal(t, 12, opts.Threads)

	opts = programEncoderOpts(AppConfig{
		X264Threads:   x264Threads{N: 16, Source: "flag"},
		OutputBitrate: 8_000_000,
		OutputPreset:  "quality",
	})
	require.Equal(t, &codec.EncoderOptions{Bitrate: 8_000_000, Preset: codec.OutputPresetQuality, Threads: 16}, opts)
}

func TestX264ThreadsCollector(t *testing.T) {
	c := newX264ThreadsCollector(x264Threads{N: 16, Source: "cgroup", Cores: 28})
	require.Equal(t, map[string]float64{"cgroup": 16},
		metricstest.Series(t, c)["switchframe_program_encoder_threads"])
}
