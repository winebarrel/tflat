package tflat

import (
	"fmt"
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2/hclwrite"
)

// movedEntry is a single moved block we need to emit.
type movedEntry struct {
	from string // e.g. "module.web.aws_s3_bucket.this" or "module.web.module.inner.aws_iam_role.role"
	to   string // e.g. "aws_s3_bucket.web_this"
}

// collectMovedForCall produces moved entries for one (possibly nested) call.
// modulePath is the chain of module names from root, e.g. ["web", "inner"].
// dir is the module directory. dirs is the modules.json lookup.
func collectMovedForCall(modulePath []string, moduleKey string, dirs map[string]string) ([]movedEntry, error) {
	dir, ok := dirs[moduleKey]
	if !ok {
		return nil, fmt.Errorf("module %q not found in .terraform/modules/modules.json", moduleKey)
	}
	lm, err := loadModule(dir)
	if err != nil {
		return nil, err
	}
	prefix := strings.Join(modulePath, "_")
	modPrefix := "module." + strings.Join(modulePath, ".module.")

	var entries []movedEntry
	// Stable order over resource addresses.
	addrs := make([]string, 0, len(lm.resourceAddrs))
	for a := range lm.resourceAddrs {
		addrs = append(addrs, a)
	}
	sort.Strings(addrs)
	for _, addr := range addrs {
		var from, to string
		if strings.HasPrefix(addr, "data.") {
			// addr = "data.TYPE.NAME"
			parts := strings.SplitN(addr, ".", 3)
			from = modPrefix + "." + addr
			to = "data." + parts[1] + "." + prefix + "_" + parts[2]
		} else {
			parts := strings.SplitN(addr, ".", 2)
			from = modPrefix + "." + addr
			to = parts[0] + "." + prefix + "_" + parts[1]
		}
		entries = append(entries, movedEntry{from: from, to: to})
	}

	// Recurse into nested module calls.
	for _, name := range lm.moduleCallNames {
		nested, err := collectMovedForCall(append(append([]string{}, modulePath...), name), moduleKey+"."+name, dirs)
		if err != nil {
			return nil, err
		}
		entries = append(entries, nested...)
	}
	return entries, nil
}

// buildMovedFile assembles all entries into a single hclwrite.File body.
func buildMovedFile(entries []movedEntry) *hclwrite.File {
	f := hclwrite.NewEmptyFile()
	body := f.Body()
	for _, e := range entries {
		blk := body.AppendNewBlock("moved", nil)
		blk.Body().SetAttributeRaw("from", traversalTokens(e.from))
		blk.Body().SetAttributeRaw("to", traversalTokens(e.to))
	}
	return f
}

// traversalTokens builds tokens for a dotted address like "module.web.aws_s3_bucket.this".
func traversalTokens(addr string) hclwrite.Tokens {
	parts := strings.Split(addr, ".")
	out := hclwrite.Tokens{}
	for i, p := range parts {
		if i > 0 {
			out = append(out, dotToken())
		}
		out = append(out, identToken(p))
	}
	return out
}
