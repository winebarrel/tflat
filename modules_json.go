package tflat

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// modulesJson mirrors .terraform/modules/modules.json.
type modulesJson struct {
	Modules []moduleRecord `json:"Modules"`
}

type moduleRecord struct {
	Key    string `json:"Key"`
	Source string `json:"Source"`
	Dir    string `json:"Dir"`
}

// loadModulesJson reads .terraform/modules/modules.json under rootDir and
// returns a map from module key ("web", "web.inner", ...) to absolute directory.
func loadModulesJson(rootDir string) (map[string]string, error) {
	path := filepath.Join(rootDir, ".terraform", "modules", "modules.json")
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	var mj modulesJson
	if err := json.Unmarshal(b, &mj); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	out := map[string]string{}
	for _, m := range mj.Modules {
		dir := m.Dir
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(rootDir, dir)
		}
		out[m.Key] = dir
	}
	return out, nil
}
