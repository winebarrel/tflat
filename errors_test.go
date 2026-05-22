package tflat_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/winebarrel/tflat"
)

// writeTestTree builds a directory with the given files. Keys are paths
// relative to the root.
func writeTestTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for path, content := range files {
		full := filepath.Join(root, path)
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0755))
		require.NoError(t, os.WriteFile(full, []byte(content), 0644))
	}
	return root
}

func TestFlatten_MissingModulesJson(t *testing.T) {
	root := writeTestTree(t, map[string]string{
		"main.tf": `module "x" { source = "./modules/x" }`,
	})
	_, err := tflat.Flatten(&tflat.Options{Dir: root})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "modules.json")
}

func TestFlatten_ModuleKeyNotFound(t *testing.T) {
	root := writeTestTree(t, map[string]string{
		"main.tf": `module "missing" {
  source = "./modules/missing"
}`,
		".terraform/modules/modules.json": `{"Modules":[{"Key":"","Source":"","Dir":"."}]}`,
	})
	_, err := tflat.Flatten(&tflat.Options{Dir: root})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `module "missing" not found`)
}

func TestFlatten_HCLParseError(t *testing.T) {
	root := writeTestTree(t, map[string]string{
		"main.tf":                         `this is = not = valid`,
		".terraform/modules/modules.json": `{"Modules":[{"Key":"","Source":"","Dir":"."}]}`,
	})
	_, err := tflat.Flatten(&tflat.Options{Dir: root})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse")
}

func TestFlatten_ModulesJsonInvalid(t *testing.T) {
	root := writeTestTree(t, map[string]string{
		"main.tf":                         `# empty`,
		".terraform/modules/modules.json": `{not json`,
	})
	_, err := tflat.Flatten(&tflat.Options{Dir: root})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse")
}

func TestFlatten_OverrideFileRejected(t *testing.T) {
	root := writeTestTree(t, map[string]string{
		"main.tf":                         `# empty`,
		"override.tf":                     `# noop`,
		".terraform/modules/modules.json": `{"Modules":[{"Key":"","Source":"","Dir":"."}]}`,
	})
	_, err := tflat.Flatten(&tflat.Options{Dir: root})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "override")
}

func TestFlatten_NoOverrideFileInModule(t *testing.T) {
	root := writeTestTree(t, map[string]string{
		"main.tf": `module "m" { source = "./modules/m" }`,
		"modules/m/main.tf": `resource "aws_s3_bucket" "this" {
  bucket = "x"
}`,
		"modules/m/foo_override.tf": `# noop`,
		".terraform/modules/modules.json": `{"Modules":[
  {"Key":"","Source":"","Dir":"."},
  {"Key":"m","Source":"./modules/m","Dir":"modules/m"}
]}`,
	})
	_, err := tflat.Flatten(&tflat.Options{Dir: root})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "override")
}

func TestFlatten_DefaultsAppliedWhenZeroValue(t *testing.T) {
	root := writeTestTree(t, map[string]string{
		"main.tf":                         `# empty`,
		".terraform/modules/modules.json": `{"Modules":[{"Key":"","Source":"","Dir":"."}]}`,
	})
	// Pass empty MovedFile to exercise the default-fill branch.
	res, err := tflat.Flatten(&tflat.Options{Dir: root})
	require.NoError(t, err)
	assert.NotNil(t, res)
}

func TestFlatten_DirDefaults(t *testing.T) {
	// Empty Dir is filled with ".". To exercise without polluting cwd we
	// chdir into a temp tree first.
	root := writeTestTree(t, map[string]string{
		"main.tf":                         `# empty`,
		".terraform/modules/modules.json": `{"Modules":[{"Key":"","Source":"","Dir":"."}]}`,
	})
	cwd, err := os.Getwd()
	require.NoError(t, err)
	defer os.Chdir(cwd)
	require.NoError(t, os.Chdir(root))
	res, err := tflat.Flatten(&tflat.Options{})
	require.NoError(t, err)
	assert.NotNil(t, res)
}
