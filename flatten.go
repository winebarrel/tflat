package tflat

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/hcl/v2/hclwrite"
)

// Result is the collection of files produced or rewritten.
// Paths are relative to Options.Dir.
type Result struct {
	// Files lists rewritten parent files and new <callname>.tf / moved.tf files.
	Files []FileOutput
}

type FileOutput struct {
	Path    string
	Content []byte
}

// pendingCall pairs a module call with its flattened result.
type pendingCall struct {
	mc   *moduleCall
	flat *flattened
}

// WriteToDir writes each file in the result into dir using its relative path.
// Existing files keep their permission bits; new files are created with 0644.
// Absolute paths and paths that escape dir via `..` are rejected.
func (r *Result) WriteToDir(dir string) error {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	for _, f := range r.Files {
		if filepath.IsAbs(f.Path) {
			return fmt.Errorf("WriteToDir: refusing absolute path %q", f.Path)
		}
		path := filepath.Join(absDir, f.Path)
		absPath, err := filepath.Abs(path)
		if err != nil {
			return err
		}
		if !pathInside(absDir, absPath) {
			return fmt.Errorf("WriteToDir: path %q escapes target directory", f.Path)
		}
		mode := os.FileMode(0644)
		if info, err := os.Stat(absPath); err == nil {
			mode = info.Mode().Perm()
		}
		if err := os.WriteFile(absPath, f.Content, mode); err != nil {
			return err
		}
	}
	return nil
}

// pathInside reports whether child is the same as parent or sits below it.
// Both paths must be absolute and cleaned. Uses filepath.Rel to handle the
// filesystem root case where HasPrefix(child, parent+sep) would build "//".
func pathInside(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	if rel == ".." {
		return false
	}
	return !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// WriteToStdout writes each file to w prefixed by a `# === path ===` banner.
func (r *Result) WriteToStdout(w io.Writer) error {
	for i, f := range r.Files {
		if i > 0 {
			if _, err := fmt.Fprintln(w); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintf(w, "# === %s ===\n", f.Path); err != nil {
			return err
		}
		if _, err := w.Write(f.Content); err != nil {
			return err
		}
	}
	return nil
}

// Flatten loads the root directory and returns the flattened files.
// It does not write anything; the caller decides what to do with the result.
func Flatten(opts *Options) (*Result, error) {
	if opts.Dir == "" {
		opts.Dir = "."
	}
	if opts.MovedFile == "" {
		opts.MovedFile = "moved.tf"
	}
	dirs, err := loadModulesJson(opts.Dir)
	if err != nil {
		return nil, err
	}
	rootFiles, err := parseDir(opts.Dir)
	if err != nil {
		return nil, err
	}

	// Collect top-level module calls in source order across files.
	var pending []*pendingCall
	for _, pf := range rootFiles {
		for _, mc := range extractModuleCalls(pf) {
			pending = append(pending, &pendingCall{mc: mc})
		}
	}

	// Flatten each call.
	parentModuleSubst := map[string]hclwrite.Tokens{} // "name.output" -> tokens
	callsByName := map[string]*pendingCall{}
	for _, p := range pending {
		fl, err := flattenCall(p.mc, p.mc.name, p.mc.name, dirs)
		if err != nil {
			return nil, fmt.Errorf("flatten module %q: %w", p.mc.name, err)
		}
		p.flat = fl
		callsByName[p.mc.name] = p

		// Output expressions are already rewritten in the module's scope.
		for outName, toks := range fl.outputs {
			parentModuleSubst[p.mc.name+"."+outName] = toks
		}
	}

	// Resolve cross-module references inside the outputs map. An output of
	// module B may reference module.a.x, which expands to a's renamed
	// resource ref. Iterate until a fixpoint, bounded by the number of
	// calls so chains terminate even on a user-written cycle.
	{
		tmpRW := &rewriter{modules: parentModuleSubst}
		for i := 0; i < len(parentModuleSubst)+1; i++ {
			changed := false
			for key, toks := range parentModuleSubst {
				newToks := tmpRW.rewriteTokens(toks)
				if !tokensEqual(newToks, toks) {
					parentModuleSubst[key] = newToks
					changed = true
				}
			}
			if !changed {
				break
			}
		}
	}

	// Parent-side rewriter, aware of all top-level modules' outputs.
	// Resource renames at the parent level are already applied inside
	// flattened outputs.
	parentRW := &rewriter{
		modules: parentModuleSubst,
	}

	// Second pass: emitted blocks may still contain raw module.X.Y
	// references (cross-module args, count/for_each that read from a
	// sibling module). Re-run the rewriter with the fully-resolved module
	// map.
	for _, p := range pending {
		for _, b := range p.flat.blocks {
			rewriteBodyInPlace(b.Body(), parentRW)
		}
	}

	// Every emitted resource/data block must have a unique address.
	// Terraform would reject otherwise.
	if err := checkAddressCollisions(rootFiles, pending); err != nil {
		return nil, err
	}

	out := &Result{}

	// Rewrite parent files: comment out module blocks and substitute
	// module.X.Y references in attribute expressions.
	for _, pf := range rootFiles {
		newContent, changed := rewriteParentFile(pf, parentRW)
		if changed {
			out.Files = append(out.Files, FileOutput{
				Path:    pf.name,
				Content: newContent,
			})
		}
	}

	// Emit one file per top-level module call, sorted by name.
	names := make([]string, 0, len(callsByName))
	for n := range callsByName {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		p := callsByName[name]
		if len(p.flat.blocks) == 0 {
			// Module contributed nothing (variables/outputs/provider only,
			// or empty). Skip the <callname>.tf.
			continue
		}
		f := hclwrite.NewEmptyFile()
		body := f.Body()
		for _, b := range p.flat.blocks {
			body.AppendBlock(b)
			body.AppendNewline()
		}
		out.Files = append(out.Files, FileOutput{
			Path:    name + ".tf",
			Content: hclwrite.Format(f.Bytes()),
		})
	}

	// Emit moved.tf collecting entries for all top-level calls.
	var allMoved []movedEntry
	for _, name := range names {
		entries, err := collectMovedForCall([]string{name}, name, dirs)
		if err != nil {
			return nil, err
		}
		allMoved = append(allMoved, entries...)
	}
	if len(allMoved) > 0 {
		mf := buildMovedFile(allMoved)
		out.Files = append(out.Files, FileOutput{
			Path:    opts.MovedFile,
			Content: hclwrite.Format(mf.Bytes()),
		})
	}

	return out, nil
}

// rewriteParentFile produces the rewritten content for one parent .tf file.
//  1. Non-module top-level blocks have their attribute expressions rewritten
//     in place (`module.X.Y` becomes the inlined output expression).
//  2. Each `module "X" {}` block is replaced with a `#`-prefixed comment so
//     the original survives in-source for review.
//
// The implementation mutates the parsed file in place (preserving top-level
// comments and blank lines), then does a byte-level substitution to comment
// out module blocks. No internal markers are used since they could collide
// with user content.
//
// Returns the new bytes and whether the file changed.
func rewriteParentFile(pf *parsedFile, rw *rewriter) ([]byte, bool) {
	src := pf.file.Bytes()

	// Step 1: mutate every non-module top-level block's body in place.
	hasModule := false
	mutated := false
	for _, b := range pf.file.Body().Blocks() {
		if b.Type() == "module" && len(b.Labels()) == 1 {
			hasModule = true
			continue
		}
		before := b.BuildTokens(nil)
		rewriteBodyInPlace(b.Body(), rw)
		if !tokensEqual(before, b.BuildTokens(nil)) {
			mutated = true
		}
	}

	if !hasModule && !mutated {
		return src, false
	}

	rewritten := hclwrite.Format(pf.file.Bytes())

	// Step 2: comment out each module block via byte-level transformation
	// based on hclsyntax source ranges. Re-parse the rewritten bytes so
	// positions reflect any reflows from formatting.
	if !hasModule {
		return rewritten, true
	}
	sf, diags := hclsyntax.ParseConfig(rewritten, pf.path, hcl.InitialPos)
	if diags.HasErrors() {
		// Unreachable: hclwrite emits via the same lexer. Fall back to
		// returning the rewritten file without commenting out. Still valid
		// HCL, just missing the audit trail.
		return rewritten, true
	}
	body, ok := sf.Body.(*hclsyntax.Body)
	if !ok {
		return rewritten, true
	}

	type byteRange struct{ start, end int }
	var moduleRanges []byteRange
	for _, blk := range body.Blocks {
		if blk.Type != "module" || len(blk.Labels) != 1 {
			continue
		}
		moduleRanges = append(moduleRanges, byteRange{
			start: blk.DefRange().Start.Byte,
			end:   blk.CloseBraceRange.End.Byte,
		})
	}
	// Apply in descending order so byte positions remain valid as we splice.
	sort.Slice(moduleRanges, func(i, j int) bool {
		return moduleRanges[i].start > moduleRanges[j].start
	})
	out := rewritten
	for _, r := range moduleRanges {
		commented := commentOutBytes(out[r.start:r.end])
		out = append(append(append([]byte{}, out[:r.start]...), commented...), out[r.end:]...)
	}
	return out, true
}

// commentOutBytes prefixes every line in b with `# ` (empty lines get just `#`).
func commentOutBytes(b []byte) []byte {
	lines := bytes.Split(b, []byte{'\n'})
	for i, line := range lines {
		if len(line) == 0 {
			lines[i] = []byte{'#'}
		} else {
			lines[i] = append([]byte("# "), line...)
		}
	}
	return bytes.Join(lines, []byte{'\n'})
}

// resourceAddr returns "TYPE.NAME" for a resource block or "data.TYPE.NAME"
// for a data block. Returns "" if the block has the wrong number of labels.
func resourceAddr(b *hclwrite.Block) string {
	labels := b.Labels()
	if len(labels) != 2 {
		return ""
	}
	if b.Type() == "data" {
		return "data." + labels[0] + "." + labels[1]
	}
	return labels[0] + "." + labels[1]
}

// addrOwner records where a resource/data address was declared, so
// collision diagnostics can point at the source location.
type addrOwner struct {
	desc string    // e.g. `parent file main.tf` or `module call "m"`
	loc  hcl.Range // source range; .Filename == "" if unknown
}

func (o addrOwner) String() string {
	if o.loc.Filename != "" {
		return o.desc + " at " + formatRange(o.loc)
	}
	return o.desc
}

// checkAddressCollisions scans every resource/data block in the flattened
// project (parent files plus inlined module bodies) and errors if any two
// share the same Terraform address.
//
// Diagnostics include the source range of both colliding declarations. For
// parent blocks the range points at the block header. For module-supplied
// blocks the range points at the module call, since the renamed block no
// longer corresponds to a single named position in the module body.
func checkAddressCollisions(rootFiles []*parsedFile, pending []*pendingCall) error {
	owner := map[string]addrOwner{}
	for _, pf := range rootFiles {
		if pf.syntax == nil {
			continue
		}
		// Walk hclsyntax (not hclwrite) so each block carries its own
		// SrcRange. findBlockRange would return the first match in the
		// file for every duplicate, hiding the second location.
		for _, blk := range pf.syntax.Blocks {
			if blk.Type != "resource" && blk.Type != "data" {
				continue
			}
			if len(blk.Labels) != 2 {
				continue
			}
			var addr string
			if blk.Type == "data" {
				addr = "data." + blk.Labels[0] + "." + blk.Labels[1]
			} else {
				addr = blk.Labels[0] + "." + blk.Labels[1]
			}
			entry := addrOwner{
				desc: "parent file " + pf.name,
				loc:  blk.DefRange(),
			}
			if prev, ok := owner[addr]; ok {
				return fmt.Errorf(
					"address %q is declared twice in the parent:\n"+
						"  first occurrence: %s\n"+
						"  second occurrence: %s\n"+
						"  hint: terraform itself would reject this; tflat refuses to flatten until the duplicate is resolved",
					addr, prev, entry,
				)
			}
			owner[addr] = entry
		}
	}
	for _, p := range pending {
		for _, b := range p.flat.blocks {
			if b.Type() != "resource" && b.Type() != "data" {
				continue
			}
			addr := resourceAddr(b)
			if addr == "" {
				continue
			}
			entry := addrOwner{
				desc: fmt.Sprintf("module call %q", p.mc.name),
				loc:  findBlockRange(p.mc.parentPF, "module", []string{p.mc.name}),
			}
			if prev, ok := owner[addr]; ok {
				return fmt.Errorf(
					"address %q would collide after flattening:\n"+
						"  first occurrence: %s\n"+
						"  second occurrence: %s\n"+
						"  hint: rename one of the resources to avoid the prefix-rename collision",
					addr, prev, entry,
				)
			}
			owner[addr] = entry
		}
	}
	return nil
}
