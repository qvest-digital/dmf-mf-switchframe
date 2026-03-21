package stmap

import (
	"fmt"
	"sort"
)

// GeneratorInfo describes a registered generator with its name and parameter
// definitions. Used by both static and animated generators.
type GeneratorInfo struct {
	Name        string               `json:"name"`
	Description string               `json:"description"`
	Type        string               `json:"type"` // "static" or "animated"
	Params      map[string]ParamInfo `json:"params"`
}

// ParamInfo describes a single generator parameter with its default and range.
type ParamInfo struct {
	Description string  `json:"description"`
	Default     float64 `json:"default"`
	Min         float64 `json:"min"`
	Max         float64 `json:"max"`
}

// --- Static generators ---

// GeneratorFunc creates a static ST map from parameters.
type GeneratorFunc func(params map[string]float64, w, h int) (*STMap, error)

var generators = map[string]GeneratorFunc{}
var generatorInfos = map[string]GeneratorInfo{}

// registerGenerator registers a static generator. Called from init() functions.
func registerGenerator(info GeneratorInfo, fn GeneratorFunc) {
	generators[info.Name] = fn
	generatorInfos[info.Name] = info
}

// Generate creates a static ST map using the named generator. Unknown params
// are ignored; missing params use defaults from GeneratorInfo.
func Generate(typeName string, params map[string]float64, w, h int) (*STMap, error) {
	fn, ok := generators[typeName]
	if !ok {
		return nil, fmt.Errorf("stmap: unknown generator type %q", typeName)
	}
	resolved := resolveParams(generatorInfos[typeName], params)
	return fn(resolved, w, h)
}

// ListGenerators returns sorted names of all registered static generators.
func ListGenerators() []string {
	names := make([]string, 0, len(generators))
	for name := range generators {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// --- Animated generators ---

// AnimatedGeneratorFunc creates an animated ST map cycle from parameters.
type AnimatedGeneratorFunc func(params map[string]float64, w, h, frameCount int) (*AnimatedSTMap, error)

var animatedGenerators = map[string]AnimatedGeneratorFunc{}
var animatedGeneratorInfos = map[string]GeneratorInfo{}

// registerAnimatedGenerator registers an animated generator. Called from init() functions.
func registerAnimatedGenerator(info GeneratorInfo, fn AnimatedGeneratorFunc) {
	animatedGenerators[info.Name] = fn
	animatedGeneratorInfos[info.Name] = info
}

// GenerateAnimated creates an animated ST map using the named generator.
// Unknown params are ignored; missing params use defaults from GeneratorInfo.
func GenerateAnimated(typeName string, params map[string]float64, w, h, frameCount int) (*AnimatedSTMap, error) {
	fn, ok := animatedGenerators[typeName]
	if !ok {
		return nil, fmt.Errorf("stmap: unknown animated generator type %q", typeName)
	}
	resolved := resolveParams(animatedGeneratorInfos[typeName], params)
	return fn(resolved, w, h, frameCount)
}

// ListAnimatedGenerators returns sorted names of all registered animated generators.
func ListAnimatedGenerators() []string {
	names := make([]string, 0, len(animatedGenerators))
	for name := range animatedGenerators {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// resolveParams fills in defaults for any missing parameters.
func resolveParams(info GeneratorInfo, params map[string]float64) map[string]float64 {
	resolved := make(map[string]float64, len(info.Params))
	for name, p := range info.Params {
		resolved[name] = p.Default
	}
	for k, v := range params {
		resolved[k] = v
	}
	return resolved
}

// GeneratorInfoList returns all generator info (static and animated) sorted by name.
func GeneratorInfoList() []GeneratorInfo {
	infos := make([]GeneratorInfo, 0, len(generatorInfos)+len(animatedGeneratorInfos))
	for _, info := range generatorInfos {
		infos = append(infos, info)
	}
	for _, info := range animatedGeneratorInfos {
		infos = append(infos, info)
	}
	sort.Slice(infos, func(i, j int) bool {
		return infos[i].Name < infos[j].Name
	})
	return infos
}

// paramOr returns params[key] if present, otherwise def.
func paramOr(params map[string]float64, key string, def float64) float64 {
	if params == nil {
		return def
	}
	if v, ok := params[key]; ok {
		return v
	}
	return def
}
