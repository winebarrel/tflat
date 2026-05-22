variable "items" {
  type = list(string)
}

resource "aws_s3_bucket" "this" {
  for_each = toset(var.items)
  bucket   = each.value
}
