package tflat

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/hcl/v2/hclwrite"
)

// parsedFile is a parsed .tf file. It keeps an hclwrite representation for
// token-level rewriting and an hclsyntax body for source position info
// used in diagnostics.
type parsedFile struct {
	path   string // absolute path
	name   string // base name (e.g. "main.tf")
	file   *hclwrite.File
	syntax *hclsyntax.Body
}

// loadedModule represents the parsed contents of a single module directory.
type loadedModule struct {
	dir   string
	files []*parsedFile

	// variables maps name to default expression tokens (nil if no default).
	variables map[string]hclwrite.Tokens
	// outputs maps name to value expression tokens.
	outputs map[string]hclwrite.Tokens
	// resourceAddrs lists the TYPE.NAME addresses declared in the module.
	// Data sources are keyed with a "data." prefix.
	resourceAddrs map[string]bool
	// moduleCallNames lists nested module call names declared in this dir.
	moduleCallNames []string
}

// parseDir reads all *.tf files from dir, ignoring nested directories.
func parseDir(dir string) ([]*parsedFile, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read dir %s: %w", dir, err)
	}
	var out []*parsedFile
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".tf") {
			continue
		}
		// Override files use merge semantics and complicate flattening.
		if strings.HasSuffix(name, "_override.tf") || name == "override.tf" {
			return nil, fmt.Errorf("override files are not supported: %s", name)
		}
		path := filepath.Join(dir, name)
		src, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		f, diags := hclwrite.ParseConfig(src, path, hcl.InitialPos)
		if diags.HasErrors() {
			return nil, fmt.Errorf("parse %s: %s", path, diags.Error())
		}
		sf, sdiags := hclsyntax.ParseConfig(src, path, hcl.InitialPos)
		if sdiags.HasErrors() {
			return nil, fmt.Errorf("parse %s: %s", path, sdiags.Error())
		}
		body, _ := sf.Body.(*hclsyntax.Body)
		out = append(out, &parsedFile{path: path, name: name, file: f, syntax: body})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out, nil
}

// findAttrRange returns the source range of attrName inside a top-level
// block of the given type whose labels match. Pass nil or empty labels to
// skip the labels check. Returns an empty range if not found.
func findAttrRange(pf *parsedFile, blockType string, labels []string, attrName string) hcl.Range {
	if pf == nil || pf.syntax == nil {
		return hcl.Range{}
	}
	for _, blk := range pf.syntax.Blocks {
		if blk.Type != blockType {
			continue
		}
		if labels != nil && !labelsMatch(blk.Labels, labels) {
			continue
		}
		if attr, ok := blk.Body.Attributes[attrName]; ok {
			return attr.SrcRange
		}
	}
	return hcl.Range{}
}

// findBlockRange returns the source range of a top-level block with the
// given type and labels. Returns an empty range if not found.
func findBlockRange(pf *parsedFile, blockType string, labels []string) hcl.Range {
	if pf == nil || pf.syntax == nil {
		return hcl.Range{}
	}
	for _, blk := range pf.syntax.Blocks {
		if blk.Type != blockType {
			continue
		}
		if labels != nil && !labelsMatch(blk.Labels, labels) {
			continue
		}
		return blk.DefRange()
	}
	return hcl.Range{}
}

// findBlockRangeIn searches multiple parsedFiles for the first matching
// block. Used to point at variable declarations in any of a module's files.
func findBlockRangeIn(files []*parsedFile, blockType string, labels []string) hcl.Range {
	for _, pf := range files {
		if r := findBlockRange(pf, blockType, labels); r.Filename != "" {
			return r
		}
	}
	return hcl.Range{}
}

func labelsMatch(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// formatRange renders a Range as "file:line:col", or just "file" if line is 0.
func formatRange(r hcl.Range) string {
	if r.Filename == "" {
		return "<unknown>"
	}
	if r.Start.Line == 0 {
		return r.Filename
	}
	return fmt.Sprintf("%s:%d:%d", r.Filename, r.Start.Line, r.Start.Column)
}

// loadModule parses dir and extracts variables/outputs/resources metadata.
func loadModule(dir string) (*loadedModule, error) {
	files, err := parseDir(dir)
	if err != nil {
		return nil, err
	}
	m := &loadedModule{
		dir:           dir,
		files:         files,
		variables:     map[string]hclwrite.Tokens{},
		outputs:       map[string]hclwrite.Tokens{},
		resourceAddrs: map[string]bool{},
	}
	for _, pf := range files {
		for _, b := range pf.file.Body().Blocks() {
			switch b.Type() {
			case "variable":
				labels := b.Labels()
				if len(labels) == 0 {
					continue
				}
				name := labels[0]
				if defAttr := b.Body().GetAttribute("default"); defAttr != nil {
					m.variables[name] = cloneTokens(defAttr.Expr().BuildTokens(nil))
				} else {
					m.variables[name] = nil
				}
			case "output":
				labels := b.Labels()
				if len(labels) == 0 {
					continue
				}
				name := labels[0]
				if valAttr := b.Body().GetAttribute("value"); valAttr != nil {
					m.outputs[name] = cloneTokens(valAttr.Expr().BuildTokens(nil))
				}
			case "resource":
				labels := b.Labels()
				if len(labels) == 2 {
					m.resourceAddrs[labels[0]+"."+labels[1]] = true
				}
			case "data":
				labels := b.Labels()
				if len(labels) == 2 {
					m.resourceAddrs["data."+labels[0]+"."+labels[1]] = true
				}
			case "module":
				labels := b.Labels()
				if len(labels) == 1 {
					m.moduleCallNames = append(m.moduleCallNames, labels[0])
				}
			}
		}
	}
	return m, nil
}
