package tflat

import (
	"bytes"
	"fmt"
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
	type pendingCall struct {
		mc   *moduleCall
		flat *flattened
	}
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
		if b.Type() == "module" {
			hasModule = true
			// Insert a placeholder we will substitute with commented-out text.
			marker := fmt.Sprintf("__TFLAT_MODULE_BLOCK_%s__", b.Labels()[0])
			rb.AppendUnstructuredTokens(hclwrite.Tokens{
				{Type: 0, Bytes: []byte("#:" + marker + "\n")},
			})
			continue
		}
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
		if b.Type() != "module" {
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
