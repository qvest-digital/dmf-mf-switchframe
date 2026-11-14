package main

import (
	"fmt"
	"strconv"
	"strings"
)

// engineLabelPodIndex is the --engine-label that takes the label from the
// pod's ordinal in its StatefulSet: pod 0 is engine a, pod 1 engine b. Both
// engines of a pair then run from one pod template.
const engineLabelPodIndex = "pod-index"

// podIndexEnv names the variable a StatefulSet pod's ordinal is read from,
// set from the pod's apps.kubernetes.io/pod-index label through the
// downward API. The host name is not read instead: with hostNetwork, or a
// hostname set in the pod spec, it is not the pod's name, and a suffix such
// as a node's would give two engines one label.
const podIndexEnv = "SWITCHFRAME_POD_INDEX"

// The placeholders a per-engine setting may hold, replaced at start by this
// engine's label and its peer's.
const (
	enginePlaceholder = "{engine}"
	peerPlaceholder   = "{peer}"
)

// resolveEngineLabel returns the engine label --engine-label names: label
// itself, or for pod-index the label of the pod's ordinal.
func resolveEngineLabel(label string, getenv func(string) string) (string, error) {
	if label != engineLabelPodIndex {
		return label, nil
	}
	index := getenv(podIndexEnv)
	if index == "" {
		return "", fmt.Errorf("--engine-label=%s needs %s, the pod's apps.kubernetes.io/pod-index label", engineLabelPodIndex, podIndexEnv)
	}
	n, err := strconv.ParseUint(index, 10, 8)
	if err != nil {
		return "", fmt.Errorf("--engine-label=%s: %s=%q is not a number", engineLabelPodIndex, podIndexEnv, index)
	}
	switch n {
	case 0:
		return "a", nil
	case 1:
		return "b", nil
	}
	return "", fmt.Errorf("--engine-label=%s: pod index %d: a pair has pods 0 and 1", engineLabelPodIndex, n)
}

// expandEngine replaces {engine} and {peer} in the setting named name with
// the engine's label and its peer's. A placeholder needs a label.
func expandEngine(name, value, label string) (string, error) {
	if !strings.Contains(value, enginePlaceholder) && !strings.Contains(value, peerPlaceholder) {
		return value, nil
	}
	if label != "a" && label != "b" {
		return "", fmt.Errorf("%s %q: %s and %s need --engine-label a, b or %s", name, value, enginePlaceholder, peerPlaceholder, engineLabelPodIndex)
	}
	return strings.NewReplacer(enginePlaceholder, label, peerPlaceholder, peerEngineLabel(label)).Replace(value), nil
}

// expandEngineSettings resolves the placeholders in every setting that
// differs between the engines of a pair, so one set of flags and variables
// serves both. The SRT listen address, the clip library and the recording
// archive are the same for both engines and are not expanded: each pod has
// an address of its own, and the archive files each engine's recordings
// under its label.
func (cfg *AppConfig) expandEngineSettings() error {
	for _, s := range []struct {
		name  string
		value *string
	}{
		{"--peer-engine-url", &cfg.PeerEngineURL},
		{"--peer-public-url", &cfg.PeerPublicURL},
		{"--webtransport-host", &cfg.WebTransportHost},
		{"--tls-cert", &cfg.TLSCert},
		{"--tls-key", &cfg.TLSKey},
		{"--mxl-output", &cfg.MXLOutput},
		{"--mxl-output-video-def", &cfg.MXLOutputVideoDef},
		{"--mxl-output-audio-def", &cfg.MXLOutputAudioDef},
		{"--mxl-nmos-host", &cfg.MXLNMOS.Host},
		{"--mxl-nmos-peer-output-video-def", &cfg.MXLNMOS.PeerOutputVideoDef},
		{"--mxl-nmos-peer-output-audio-def", &cfg.MXLNMOS.PeerOutputAudioDef},
		{"--outputs-config", &cfg.OutputsConfig},
		{"SWITCHFRAME_STATE_DIR", &cfg.StateDir},
	} {
		v, err := expandEngine(s.name, *s.value, cfg.EngineLabel)
		if err != nil {
			return err
		}
		*s.value = v
	}
	return nil
}
