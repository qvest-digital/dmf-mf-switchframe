package main

import (
	"fmt"
	"log/slog"

	"github.com/zsiec/switchframe/server/output"
)

// initStartupOutputs starts the outputs --outputs-config declares. A file that
// does not load or an output the manager refuses ends startup, so a deployment
// that declared an output never runs without it.
func (a *App) initStartupOutputs() error {
	if a.cfg.OutputsConfig == "" {
		return nil
	}
	cfg, err := output.LoadStartupConfig(a.cfg.OutputsConfig)
	if err != nil {
		return err
	}
	if err := a.outputMgr.ApplyStartup(cfg); err != nil {
		return fmt.Errorf("outputs config: %w", err)
	}
	if cfg.Recording != nil {
		rc, _ := cfg.Recording.RecorderConfig()
		a.startupRecordingDir = rc.Dir
	}
	slog.Info("startup outputs started",
		"destinations", len(cfg.Destinations),
		"recording", cfg.Recording != nil)
	return nil
}
