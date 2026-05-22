variable "required" {
  type = string
}

resource "aws_s3_bucket" "this" {
  bucket = var.required
}
