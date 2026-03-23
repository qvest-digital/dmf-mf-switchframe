package htmlgfx

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// DiscoverTemplates scans the templates directory for HTML5 graphics templates.
// Each subdirectory containing an index.html is treated as a template.
// OGraf templates are detected by the presence of a .ograf.json manifest.
func (m *Manager) DiscoverTemplates() []TemplateInfo {
	dir := m.config.TemplatesDir
	if dir == "" {
		return nil
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	var templates []TemplateInfo
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		tmplDir := filepath.Join(dir, entry.Name())
		indexPath := filepath.Join(tmplDir, "index.html")

		if _, err := os.Stat(indexPath); err != nil {
			continue
		}

		info := TemplateInfo{
			ID:     entry.Name(),
			Name:   dirNameToDisplayName(entry.Name()),
			Format: "casparcg",
			URL:    "file://" + indexPath,
		}

		// Check for OGraf manifest.
		manifestPath := filepath.Join(tmplDir, "manifest.ograf.json")
		if data, err := os.ReadFile(manifestPath); err == nil {
			info.Format = "ograf"
			var manifest struct {
				Name        string         `json:"name"`
				Description string         `json:"description"`
				DataSchema  map[string]any `json:"dataSchema"`
			}
			if json.Unmarshal(data, &manifest) == nil {
				if manifest.Name != "" {
					info.Name = manifest.Name
				}
				info.Description = manifest.Description
				info.DataSchema = manifest.DataSchema
			}
		}

		// Check for CasparCG manifest (with fields/actions schema).
		casparManifest := filepath.Join(tmplDir, "manifest.json")
		if data, err := os.ReadFile(casparManifest); err == nil {
			var manifest struct {
				Name        string `json:"name"`
				Description string `json:"description"`
				Fields      []any  `json:"fields"`
				Actions     []any  `json:"actions"`
			}
			if json.Unmarshal(data, &manifest) == nil {
				if manifest.Name != "" {
					info.Name = manifest.Name
				}
				if manifest.Description != "" {
					info.Description = manifest.Description
				}
				// Store entire manifest as DataSchema for the UI.
				var fullSchema map[string]any
				if json.Unmarshal(data, &fullSchema) == nil {
					info.DataSchema = fullSchema
				}
			}
		}

		templates = append(templates, info)
	}

	return templates
}

// dirNameToDisplayName converts a kebab-case directory name to a display name.
// e.g., "gsap-lower-third" → "GSAP Lower Third"
func dirNameToDisplayName(name string) string {
	words := strings.Split(name, "-")
	for i, w := range words {
		if len(w) <= 4 && w == strings.ToLower(w) {
			// Short words like "gsap", "ograf" → uppercase
			if w == "gsap" || w == "ograf" {
				words[i] = strings.ToUpper(w)
				continue
			}
		}
		if len(w) > 0 {
			words[i] = strings.ToUpper(w[:1]) + w[1:]
		}
	}
	return strings.Join(words, " ")
}
