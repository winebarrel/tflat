variable "prefix" {
  type = string
}

resource "aws_s3_bucket" "this" {
  bucket = "${var.prefix}-${count.index}"
}
