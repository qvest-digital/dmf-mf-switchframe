//go:build cgo && mxl

package mxl

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Discover lists all available MXL flows in the given domain directory.
// Scans for *.mxl-flow subdirectories, reads their NMOS IS-04 flow
// definitions, and checks whether each flow has an active writer.
func Discover(domain string) ([]FlowInfo, error) {
	inst, err := NewInstance(domain)
	if err != nil {
		return nil, fmt.Errorf("mxl discover: %w", err)
	}
	defer inst.Close()

	entries, err := os.ReadDir(domain)
	if err != nil {
		return nil, fmt.Errorf("mxl discover: read domain %q: %w", domain, err)
	}

	var flows []FlowInfo
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasSuffix(entry.Name(), ".mxl-flow") {
			continue
		}

		flowID := strings.TrimSuffix(entry.Name(), ".mxl-flow")

		// Read flow definition JSON.
		defPath := filepath.Join(domain, entry.Name(), "flow_def.json")
		defData, err := os.ReadFile(defPath)
		if err != nil {
			continue // Skip flows without readable definitions.
		}

		info, err := parseFlowDef(defData, flowID)
		if err != nil {
			continue
		}

		// Check if flow is active.
		active, err := inst.IsFlowActive(flowID)
		if err == nil {
			info.Active = active
		}

		flows = append(flows, info)
	}

	return flows, nil
}
