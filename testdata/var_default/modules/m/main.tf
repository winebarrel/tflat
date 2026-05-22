variable "name" {
  type    = string
  default = "default-name"
}

resource "aws_s3_bucket" "this" {
  bucket = var.name
}
