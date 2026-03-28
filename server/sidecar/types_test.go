package sidecar

import (
	"testing"
	"time"
)

func TestConfigWithDefaults_ZeroValues(t *testing.T) {
	cfg := Config{}.withDefaults()

	if cfg.ReadyTimeout != 30*time.Second {
		t.Errorf("ReadyTimeout = %v, want 30s", cfg.ReadyTimeout)
	}
	if cfg.ShutdownTimeout != 2*time.Second {
		t.Errorf("ShutdownTimeout = %v, want 2s", cfg.ShutdownTimeout)
	}
	if cfg.InitialBackoff != 1*time.Second {
		t.Errorf("InitialBackoff = %v, want 1s", cfg.InitialBackoff)
	}
	if cfg.MaxBackoff != 30*time.Second {
		t.Errorf("MaxBackoff = %v, want 30s", cfg.MaxBackoff)
	}
}

func TestConfigWithDefaults_PreservesOverrides(t *testing.T) {
	cfg := Config{
		Binary:          "/usr/bin/sidecar",
		Args:            []string{"--verbose"},
		ReadyTimeout:    5 * time.Second,
		ShutdownTimeout: 10 * time.Second,
		InitialBackoff:  500 * time.Millisecond,
		MaxBackoff:      60 * time.Second,
		SocketDir:       "/tmp/sockets",
	}.withDefaults()

	if cfg.Binary != "/usr/bin/sidecar" {
		t.Errorf("Binary = %q, want /usr/bin/sidecar", cfg.Binary)
	}
	if len(cfg.Args) != 1 || cfg.Args[0] != "--verbose" {
		t.Errorf("Args = %v, want [--verbose]", cfg.Args)
	}
	if cfg.ReadyTimeout != 5*time.Second {
		t.Errorf("ReadyTimeout = %v, want 5s", cfg.ReadyTimeout)
	}
	if cfg.ShutdownTimeout != 10*time.Second {
		t.Errorf("ShutdownTimeout = %v, want 10s", cfg.ShutdownTimeout)
	}
	if cfg.InitialBackoff != 500*time.Millisecond {
		t.Errorf("InitialBackoff = %v, want 500ms", cfg.InitialBackoff)
	}
	if cfg.MaxBackoff != 60*time.Second {
		t.Errorf("MaxBackoff = %v, want 60s", cfg.MaxBackoff)
	}
	if cfg.SocketDir != "/tmp/sockets" {
		t.Errorf("SocketDir = %q, want /tmp/sockets", cfg.SocketDir)
	}
}

func TestConfigWithDefaults_CallbacksPreserved(t *testing.T) {
	statusCalled := false
	restartCalled := false

	cfg := Config{
		OnStatus: func(s Status) {
			statusCalled = true
		},
		OnRestart: func(gen int64) {
			restartCalled = true
		},
	}.withDefaults()

	if cfg.OnStatus == nil {
		t.Fatal("OnStatus should not be nil")
	}
	if cfg.OnRestart == nil {
		t.Fatal("OnRestart should not be nil")
	}

	cfg.OnStatus(Status{})
	cfg.OnRestart(1)

	if !statusCalled {
		t.Error("OnStatus callback was not invoked")
	}
	if !restartCalled {
		t.Error("OnRestart callback was not invoked")
	}
}

func TestStatus_ZeroValue(t *testing.T) {
	var s Status
	if s.Alive {
		t.Error("zero Status.Alive should be false")
	}
	if s.Generation != 0 {
		t.Errorf("zero Status.Generation = %d, want 0", s.Generation)
	}
	if s.Restarts != 0 {
		t.Errorf("zero Status.Restarts = %d, want 0", s.Restarts)
	}
	if s.LastError != "" {
		t.Errorf("zero Status.LastError = %q, want empty", s.LastError)
	}
}
