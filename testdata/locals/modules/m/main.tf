variable "prefix" {
  type = string
}

locals {
  full_name = "${var.prefix}-bucket"
}

resource "aws_s3_bucket" "this" {
  bucket = local.full_name
}
