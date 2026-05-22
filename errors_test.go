package tflat_test

import (
	"bytes"
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

func TestResult_WriteToDir(t *testing.T) {
	// End-to-end exercise of the in-place write path used by `tflat -i`.
	root := writeTestTree(t, map[string]string{
		"main.tf": `module "m" {
  source = "./modules/m"
  name   = "x"
}`,
		"modules/m/main.tf": `variable "name" { type = string }
resource "aws_s3_bucket" "this" { bucket = var.name }`,
		".terraform/modules/modules.json": `{"Modules":[
  {"Key":"","Source":"","Dir":"."},
  {"Key":"m","Source":"./modules/m","Dir":"modules/m"}
]}`,
	})
	res, err := tflat.Flatten(&tflat.Options{Dir: root})
	require.NoError(t, err)
	require.NoError(t, res.WriteToDir(root))

	// main.tf rewritten in place with the module block commented out.
	mainBytes, err := os.ReadFile(filepath.Join(root, "main.tf"))
	require.NoError(t, err)
	assert.Contains(t, string(mainBytes), "# module \"m\"")

	// m.tf newly written.
	mBytes, err := os.ReadFile(filepath.Join(root, "m.tf"))
	require.NoError(t, err)
	assert.Contains(t, string(mBytes), "resource \"aws_s3_bucket\" \"m_this\"")
	assert.Contains(t, string(mBytes), `"x"`)

	// moved.tf newly written.
	movedBytes, err := os.ReadFile(filepath.Join(root, "moved.tf"))
	require.NoError(t, err)
	assert.Contains(t, string(movedBytes), "from = module.m.aws_s3_bucket.this")
	assert.Contains(t, string(movedBytes), "to   = aws_s3_bucket.m_this")
}

func TestResult_WriteToDir_PreservesMode(t *testing.T) {
	// When overwriting an existing file, WriteToDir keeps the original
	// permission bits instead of clobbering them with 0644.
	if os.Getuid() == 0 {
		t.Skip("running as root: file modes aren't enforced")
	}
	tmp := t.TempDir()
	mainPath := filepath.Join(tmp, "main.tf")
	require.NoError(t, os.WriteFile(mainPath, []byte("# placeholder"), 0644))
	// Force the mode explicitly: WriteFile honours umask which would strip
	// group-write on most systems otherwise.
	require.NoError(t, os.Chmod(mainPath, 0664))

	res := &tflat.Result{Files: []tflat.FileOutput{
		{Path: "main.tf", Content: []byte("# rewritten")},
		{Path: "new.tf", Content: []byte("# new")},
	}}
	require.NoError(t, res.WriteToDir(tmp))

	info, err := os.Stat(mainPath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0664), info.Mode().Perm(),
		"existing file mode must be preserved")

	info, err = os.Stat(filepath.Join(tmp, "new.tf"))
	require.NoError(t, err)
	// The default mode 0644 is subject to umask too, so accept whatever
	// 0644 turns into here — we just want the *existing* file's mode to
	// have been honoured above.
	assert.NotEqual(t, os.FileMode(0664), info.Mode().Perm(),
		"new file does not inherit the existing file's special mode")
}

func TestResult_WriteToDir_RejectsAbsolutePath(t *testing.T) {
	res := &tflat.Result{Files: []tflat.FileOutput{
		{Path: "/etc/passwd", Content: []byte("x")},
	}}
	err := res.WriteToDir(t.TempDir())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "absolute path")
}

func TestResult_WriteToDir_RejectsEscapingPath(t *testing.T) {
	// `../escape.tf` would resolve outside of the target dir.
	res := &tflat.Result{Files: []tflat.FileOutput{
		{Path: "../escape.tf", Content: []byte("x")},
	}}
	err := res.WriteToDir(t.TempDir())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "escapes target directory")
}

func TestResult_WriteToDir_NoWritablePath(t *testing.T) {
	// If the target directory doesn't exist, WriteToDir surfaces the error
	// (it does not silently swallow it).
	res := &tflat.Result{
		Files: []tflat.FileOutput{
			{Path: "x.tf", Content: []byte("# ok")},
		},
	}
	err := res.WriteToDir(filepath.Join(t.TempDir(), "does-not-exist"))
	require.Error(t, err)
}

func TestResult_WriteToStdout(t *testing.T) {
	res := &tflat.Result{
		Files: []tflat.FileOutput{
			{Path: "main.tf", Content: []byte("a\n")},
			{Path: "m.tf", Content: []byte("b\n")},
		},
	}
	var buf bytes.Buffer
	require.NoError(t, res.WriteToStdout(&buf))
	got := buf.String()
	assert.Contains(t, got, "# === main.tf ===\na\n")
	assert.Contains(t, got, "# === m.tf ===\nb\n")
	// The two files are separated by a blank line.
	assert.Regexp(t, `a\n\n# === m\.tf ===`, got)
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
