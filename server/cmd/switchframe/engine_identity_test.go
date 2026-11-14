package main

import (
	"flag"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

// parseConfigWith runs parseConfig on args with the given environment;
// a variable set to "" counts as unset.
func parseConfigWith(t *testing.T, env map[string]string, args ...string) (AppConfig, error) {
	t.Helper()
	for _, k := range []string{
		"SWITCHFRAME_ENGINE_LABEL", podIndexEnv, "SWITCHFRAME_STATE_DIR",
		"SWITCHFRAME_PEER_ENGINE_URL", "SWITCHFRAME_PEER_PUBLIC_URL",
		"SWITCHFRAME_MXL_NMOS_HOST", "SWITCHFRAME_MXL_NMOS_PEER_OUTPUT_VIDEO_DEF",
		"SWITCHFRAME_MXL_NMOS_PEER_OUTPUT_AUDIO_DEF", "SWITCHFRAME_OIDC_ISSUER",
		"SWITCHFRAME_OIDC_CLIENT_ID", "SWITCHFRAME_OIDC_REDIRECT_URL",
	} {
		t.Setenv(k, env[k])
	}
	saved, cmdLine := os.Args, flag.CommandLine
	t.Cleanup(func() { os.Args, flag.CommandLine = saved, cmdLine })
	os.Args = append([]string{"switchframe"}, args...)
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	return parseConfig()
}

func TestResolveEngineLabel(t *testing.T) {
	for _, tc := range []struct {
		name, label, index string
		want               string
		wantErr            string
	}{
		{name: "single engine", want: ""},
		{name: "a as given", label: "a", index: "1", want: "a"},
		{name: "b as given", label: "b", want: "b"},
		{name: "pod 0", label: "pod-index", index: "0", want: "a"},
		{name: "pod 1", label: "pod-index", index: "1", want: "b"},
		{name: "pod 2", label: "pod-index", index: "2", wantErr: "pod index 2"},
		{name: "index not a number", label: "pod-index", index: "b", wantErr: "not a number"},
		{name: "index unset", label: "pod-index", wantErr: podIndexEnv},
	} {
		t.Run(tc.name, func(t *testing.T) {
			getenv := func(k string) string {
				if k == podIndexEnv {
					return tc.index
				}
				return ""
			}
			got, err := resolveEngineLabel(tc.label, getenv)
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

// Both engines of a pair start from one set of flags and variables and
// differ only in the pod index: each resolves its own label, its own MXL
// program flows, outputs, state, NMOS host and sign-in callback, and its
// peer's address and program flows.
func TestParseConfig_EngineFromPodIndex(t *testing.T) {
	args := []string{
		"--engine-label=pod-index",
		"--peer-engine-url=http://sf-{peer}.ns:8081",
		"--peer-public-url=https://sf-{peer}.example.com",
		"--webtransport-host=sf-{engine}.example.com",
		"--tls-cert=/tls/{engine}/tls.crt",
		"--tls-key=/tls/{engine}/tls.key",
		"--mxl-output=program-{engine}",
		"--mxl-output-video-def=/config/program-video-{engine}.json",
		"--mxl-output-audio-def=/config/program-audio-{engine}.json",
		"--outputs-config=/config/outputs-{engine}.json",
		"--srt-listen=:9000",
	}
	env := map[string]string{
		"SWITCHFRAME_STATE_DIR":                      "/state/{engine}",
		"SWITCHFRAME_MXL_NMOS_HOST":                  "sf-{engine}.ns.svc",
		"SWITCHFRAME_MXL_NMOS_PEER_OUTPUT_VIDEO_DEF": "/config/program-video-{peer}.json",
		"SWITCHFRAME_MXL_NMOS_PEER_OUTPUT_AUDIO_DEF": "/config/program-audio-{peer}.json",
		"SWITCHFRAME_OIDC_ISSUER":                    "https://idp.example.com",
		"SWITCHFRAME_OIDC_CLIENT_ID":                 "switchframe",
		"SWITCHFRAME_OIDC_REDIRECT_URL":              "https://sf-{engine}.example.com/auth/callback",
	}
	for _, tc := range []struct{ index, engine, peer string }{
		{"0", "a", "b"},
		{"1", "b", "a"},
	} {
		t.Run(tc.engine, func(t *testing.T) {
			env[podIndexEnv] = tc.index
			cfg, err := parseConfigWith(t, env, args...)
			require.NoError(t, err)
			require.NoError(t, validateEngineLabel(cfg.EngineLabel))

			e, p := tc.engine, tc.peer
			require.Equal(t, e, cfg.EngineLabel)
			require.Equal(t, "http://sf-"+p+".ns:8081", cfg.PeerEngineURL)
			require.Equal(t, "https://sf-"+p+".example.com", cfg.PeerPublicURL)
			require.Equal(t, "sf-"+e+".example.com", cfg.WebTransportHost)
			require.Equal(t, "/tls/"+e+"/tls.crt", cfg.TLSCert)
			require.Equal(t, "/tls/"+e+"/tls.key", cfg.TLSKey)
			require.Equal(t, "program-"+e, cfg.MXLOutput)
			require.Equal(t, "/config/program-video-"+e+".json", cfg.MXLOutputVideoDef)
			require.Equal(t, "/config/program-audio-"+e+".json", cfg.MXLOutputAudioDef)
			require.Equal(t, "/config/outputs-"+e+".json", cfg.OutputsConfig)
			require.Equal(t, "/state/"+e, cfg.StateDir)
			require.Equal(t, "sf-"+e+".ns.svc", cfg.MXLNMOS.Host)
			require.Equal(t, "/config/program-video-"+p+".json", cfg.MXLNMOS.PeerOutputVideoDef)
			require.Equal(t, "/config/program-audio-"+p+".json", cfg.MXLNMOS.PeerOutputAudioDef)
			require.NotNil(t, cfg.OIDC)
			require.Equal(t, "https://sf-"+e+".example.com/auth/callback", cfg.OIDC.RedirectURL)
			require.Equal(t, ":9000", cfg.SRTListen)
		})
	}
}

// The flags a deployment of one Deployment per engine passes today parse
// as they did: an explicit label, and settings named per engine.
func TestParseConfig_ExplicitEngineLabelUnchanged(t *testing.T) {
	cfg, err := parseConfigWith(t, map[string]string{
		"SWITCHFRAME_STATE_DIR":     "/state",
		"SWITCHFRAME_MXL_NMOS_HOST": "sf-b.ns.svc",
	},
		"--engine-label=b",
		"--peer-engine-url=http://sf-a.ns:8081",
		"--mxl-output-video-def=/config/program-video-b.json",
		"--outputs-config=/config/outputs-b.json",
	)
	require.NoError(t, err)
	require.Equal(t, "b", cfg.EngineLabel)
	require.Equal(t, "http://sf-a.ns:8081", cfg.PeerEngineURL)
	require.Equal(t, "/config/program-video-b.json", cfg.MXLOutputVideoDef)
	require.Equal(t, "/config/outputs-b.json", cfg.OutputsConfig)
	require.Equal(t, "/state", cfg.StateDir)
	require.Equal(t, "sf-b.ns.svc", cfg.MXLNMOS.Host)
}

// A placeholder an engine has no label for fails start rather than naming
// a file or host that does not exist.
func TestParseConfig_PlaceholderNeedsEngineLabel(t *testing.T) {
	_, err := parseConfigWith(t, nil, "--outputs-config=/config/outputs-{engine}.json")
	require.ErrorContains(t, err, "--outputs-config")

	_, err = parseConfigWith(t, map[string]string{
		"SWITCHFRAME_OIDC_ISSUER":       "https://idp.example.com",
		"SWITCHFRAME_OIDC_CLIENT_ID":    "switchframe",
		"SWITCHFRAME_OIDC_REDIRECT_URL": "https://sf-{engine}.example.com/auth/callback",
	})
	require.ErrorContains(t, err, "--oidc-redirect-url")

	_, err = parseConfigWith(t, map[string]string{podIndexEnv: "2"}, "--engine-label=pod-index")
	require.ErrorContains(t, err, "pod index 2")
}
