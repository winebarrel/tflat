# Three required (no-default) variables; caller passes none. The first
# reported should be `alpha` because diagnostics are emitted in sorted
# variable name order regardless of source/map iteration order.
variable "zulu"  { type = string }
variable "mike"  { type = string }
variable "alpha" { type = string }

resource "aws_s3_bucket" "this" {
  bucket = "${var.alpha}-${var.mike}-${var.zulu}"
}
