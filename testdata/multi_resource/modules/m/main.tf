variable "name" {
  type = string
}

resource "aws_s3_bucket" "this" {
  bucket = var.name
}

# Second resource references the first one. After flattening both must be
# renamed and the cross-resource reference rewritten to the new name.
resource "aws_s3_bucket_policy" "p" {
  bucket = aws_s3_bucket.this.id
  policy = "{}"
}
