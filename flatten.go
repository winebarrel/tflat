package tflat

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2/hclwrite"
)

// Result is the collection of files produced (or rewritten in place).
// Paths are relative to the root directory passed in via Options.Dir.
type Result struct {
	// Files maps relative path -> file content. Includes both rewritten
	// parent files and freshly generated <callname>.tf / moved.tf files.
	Files []FileOutput
}

type FileOutput struct {
	Path    string
	Content []byte
}

// pendingCall pairs a module call with the flattened result we produced
// for it. Hoisted to file scope so checkAddressCollisions can take it.
type pendingCall struct {
	mc   *moduleCall
	flat *flattened
}

// WriteToDir writes each file in the result into dir using its relative
// path. Existing files are overwritten.
func (r *Result) WriteToDir(dir string) error {
	for _, f := range r.Files {
		path := filepath.Join(dir, f.Path)
		if err := os.WriteFile(path, f.Content, 0644); err != nil {
			return err
		}
	}
	return nil
}

// WriteToStdout writes each file to w prefixed by a `# === path ===` banner
// so a human can easily review what would be produced.
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

// Flatten loads the root directory and returns the set of files that
// represent the flattened project. It does not write anything; the caller
// (main / library user) decides what to do with the result.
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

		// Build parent-side module.X.Y -> tokens. Output expressions are
		// already rewritten in the module's scope; we use them as-is.
		for outName, toks := range fl.outputs {
			parentModuleSubst[p.mc.name+"."+outName] = toks
		}
	}

	// Resolve cross-module references inside the outputs map itself: an
	// output of module B may reference module.a.x, which should be expanded
	// to a's renamed resource ref. Iterate until a fixpoint (bounded by the
	// number of calls so chains terminate even if a user wrote a cycle).
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

	// Build the parent-side rewriter that knows all top-level modules'
	// outputs. Resource renames at the parent level are handled per-module
	// inside flattened outputs already.
	parentRW := &rewriter{
		modules: parentModuleSubst,
	}

	// Second pass: every emitted block from pass-1 may still contain raw
	// module.X.Y references (cross-module args, count/for_each that read
	// from a sibling module, etc.). Re-run the rewriter with the fully-
	// resolved module map so the final output points at the new resource
	// addresses.
	for _, p := range pending {
		for _, b := range p.flat.blocks {
			rewriteBodyInPlace(b.Body(), parentRW)
		}
	}

	// Address-collision check: every resource/data block emitted (whether
	// it stays in a parent file or moves to a <callname>.tf file) must have
	// a unique "TYPE.NAME" / "data.TYPE.NAME" address — otherwise terraform
	// will reject the output.
	if err := checkAddressCollisions(rootFiles, pending); err != nil {
		return nil, err
	}

	// Result accumulator.
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
			// or genuinely empty). No <callname>.tf to emit.
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

// rewriteParentFile produces the rewritten content for one parent .tf file:
//  1. Each `module "X" {}` block is replaced by a commented-out version.
//  2. Every other attribute expression is rewritten via the parent rewriter
//     (substituting module.X.Y references with the corresponding output
//     value expression).
//
// Returns the new bytes and whether the file effectively changed.
func rewriteParentFile(pf *parsedFile, rw *rewriter) ([]byte, bool) {
	src := pf.file.Bytes()

	// Strategy:
	// 1. Apply token-level rewrite (for module.X.Y references) by walking
	//    all top-level blocks and rewriting their attribute expressions in
	//    place using a fresh file we build up.
	// 2. While re-emitting blocks, replace module blocks with commented-out
	//    text rendered from their original source range.
	//
	// To keep formatting close to the original, we work on a string-level
	// transformation: render the file using hclwrite (rewriting non-module
	// blocks) and then comment out the module blocks via raw text.

	// Step 1: re-build a file with non-module blocks rewritten.
	rewritten := hclwrite.NewEmptyFile()
	rb := rewritten.Body()
	hasModule := false
	for _, b := range pf.file.Body().Blocks() {
		if b.Type() == "module" && len(b.Labels()) == 1 {
			hasModule = true
			// Insert a placeholder we will substitute with commented-out text.
			marker := fmt.Sprintf("__TFLAT_MODULE_BLOCK_%s__", b.Labels()[0])
			rb.AppendUnstructuredTokens(hclwrite.Tokens{
				{Type: 0, Bytes: []byte("#:" + marker + "\n")},
			})
			continue
		}
		// Non-module blocks, and malformed module blocks (zero or multiple
		// labels — syntactically valid HCL but not real module calls), are
		// copied through with attribute rewriting applied.
		nb := hclwrite.NewBlock(b.Type(), b.Labels())
		copyBodyRewritten(b.Body(), nb.Body(), rw)
		rb.AppendBlock(nb)
		rb.AppendNewline()
	}

	if !hasModule && len(parentReferencesModules(pf, rw)) == 0 {
		return src, false
	}

	formatted := hclwrite.Format(rewritten.Bytes())
	// Step 2: substitute markers with the commented-out source of each module block.
	finalBuf := bytes.NewBuffer(nil)
	finalBuf.Write(formatted)
	for _, b := range pf.file.Body().Blocks() {
		if b.Type() != "module" || len(b.Labels()) != 1 {
			continue
		}
		marker := fmt.Sprintf("#:__TFLAT_MODULE_BLOCK_%s__", b.Labels()[0])
		commented := commentOutBlock(b)
		final := strings.Replace(finalBuf.String(), marker, commented, 1)
		finalBuf.Reset()
		finalBuf.WriteString(final)
	}
	return finalBuf.Bytes(), true
}

// commentOutBlock renders block b as a `# ...`-prefixed text representation.
func commentOutBlock(b *hclwrite.Block) string {
	tmp := hclwrite.NewEmptyFile()
	tmp.Body().AppendBlock(b)
	src := strings.TrimRight(string(hclwrite.Format(tmp.Bytes())), "\n")
	lines := strings.Split(src, "\n")
	for i, line := range lines {
		if line == "" {
			lines[i] = "#"
		} else {
			lines[i] = "# " + line
		}
	}
	return strings.Join(lines, "\n")
}

// parentReferencesModules is a coarse check: does pf actually reference any
// module.X.Y that we know about? Used to decide if the file changed.
func parentReferencesModules(pf *parsedFile, rw *rewriter) []string {
	if rw.modules == nil {
		return nil
	}
	src := string(pf.file.Bytes())
	var hits []string
	for key := range rw.modules {
		if strings.Contains(src, "module."+strings.SplitN(key, ".", 2)[0]+".") {
			hits = append(hits, key)
		}
	}
	return hits
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

// checkAddressCollisions scans every resource/data block that will exist in
// the flattened project (parent files + inlined module bodies) and returns
// an error if any two share the same Terraform address.
func checkAddressCollisions(rootFiles []*parsedFile, pending []*pendingCall) error {
	owner := map[string]string{} // addr -> human-readable source
	for _, pf := range rootFiles {
		for _, b := range pf.file.Body().Blocks() {
			if b.Type() != "resource" && b.Type() != "data" {
				continue
			}
			addr := resourceAddr(b)
			if addr == "" {
				continue
			}
			owner[addr] = "parent file " + pf.name
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
			if prev, ok := owner[addr]; ok {
				return fmt.Errorf(
					"address %q would collide after flattening:\n"+
						"  first occurrence: %s\n"+
						"  second occurrence: module call %q\n"+
						"  hint: rename one of the resources to avoid the prefix-rename collision",
					addr, prev, p.mc.name,
				)
			}
			owner[addr] = "module call " + p.mc.name
		}
	}
	return nil
}
