# tflat

[![CI](https://github.com/winebarrel/tflat/actions/workflows/ci.yml/badge.svg)](https://github.com/winebarrel/tflat/actions/workflows/ci.yml)
[![codecov](https://codecov.io/gh/winebarrel/tflat/branch/main/graph/badge.svg)](https://codecov.io/gh/winebarrel/tflat)
[![AI Generated](https://img.shields.io/badge/AI%20Generated-Claude-orange?logo=anthropic)](https://claude.ai/claude-code)

`tflat` flattens Terraform `module "X" {}` calls into the parent. Each call's resources, data sources, and locals are inlined with a module-name prefix. References to `module.X.Y` in the parent are rewritten to point at the new resource address, and a `moved.tf` is generated to migrate state without recreating anything.

## Installation

```
brew install winebarrel/tflat/tflat
```

`terraform init` must be run in the target directory first, so `tflat` can read `.terraform/modules/modules.json`.

## Usage

```
Usage: tflat [<dir>] [flags]

Arguments:
  [<dir>]    Root directory containing the .tf files and .terraform/modules. Defaults to '.'.

Flags:
  -h, --help                  Show help.
  -i, --in-place              Rewrite files in-place instead of printing to stdout.
      --moved-file=STRING     Filename for the consolidated moved blocks (default: "moved.tf").
      --version
```

By default the result is printed to stdout with `# === <path> ===` banners. Pass `-i` / `--in-place` to rewrite the files on disk. Existing file permissions are preserved.

## Example

```hcl
# main.tf
module "web" {
  source = "./modules/web"
  bucket = "my-bucket"
}

resource "aws_s3_bucket_policy" "p" {
  bucket = module.web.bucket_id
  policy = "{}"
}
```

```hcl
# modules/web/main.tf
variable "bucket" {
  type = string
}

resource "aws_s3_bucket" "this" {
  bucket = var.bucket
}

output "bucket_id" {
  value = aws_s3_bucket.this.id
}
```

```sh
tflat -i .
```

```hcl
# main.tf (rewritten)
# module "web" {
#   source = "./modules/web"
#   bucket = "my-bucket"
# }
resource "aws_s3_bucket_policy" "p" {
  bucket = aws_s3_bucket.web_this.id
  policy = "{}"
}
```

```hcl
# web.tf (new)
resource "aws_s3_bucket" "web_this" {
  bucket = "my-bucket"
}
```

```hcl
# moved.tf (new)
moved {
  from = module.web.aws_s3_bucket.this
  to   = aws_s3_bucket.web_this
}
```

After running tflat, `terraform plan` should report zero changes. The `moved` block migrates state from the old module address to the new inline address.

## What gets transformed

- Resources and data sources: copied into `<callname>.tf` with the second label prefixed (`aws_s3_bucket.this` becomes `aws_s3_bucket.web_this`). Source attribute order is preserved.
- `var.X` references: replaced with the caller's argument, or the variable's default when not passed. Errors if neither is set.
- `local.X` references: renamed to `local.<callname>_X` to avoid collisions.
- `module.X.Y` references in the parent: replaced with the corresponding output expression, with `var.` and resource renames already applied.
- `module.X.Y` arguments fed into another module call: resolved by a fix-point pass after all calls are flattened.
- `count` / `for_each` on the module call: propagated onto every inlined resource. The generated `moved` block uses the key-less form so Terraform 1.1+ matches instances automatically.
- Nested `module {}` calls: expanded recursively, with prefix chaining (`outer_inner_role`).
- Original `module "X" {}` block: commented out in the parent file for audit. Top-level comments around it are kept.

## What `tflat` refuses to do

`tflat` errors out (with `file:line:col` for both sides) instead of emitting broken Terraform when:

- A required variable has no default and the caller did not provide it.
- A module call uses both `count` and `for_each`.
- A module call uses `count` or `for_each` and a resource inside the module also uses one of them. This would need a 2-D `for_each`, which Terraform does not support.
- After prefix renaming, two resources would end up at the same Terraform address (a collision between two modules, or between a parent resource and a module's renamed one).
- The parent already has two resources or data sources with the same address.
- `terraform init` was not run (no `.terraform/modules/modules.json`).
- An `override.tf` or `*_override.tf` file is present.

## Limitations

- `terraform { ... }` and `provider { ... }` blocks inside the module are stripped from the inlined output, since they would duplicate root configuration. The module call's `depends_on` and `providers` are also not propagated.
- `locals { ... }` contents are re-emitted with prefixed names. The original attribute order and comments inside the block are not preserved (other blocks preserve both).
- Propagated `count` and `for_each` attributes land at the bottom of the resource body instead of the conventional top.
