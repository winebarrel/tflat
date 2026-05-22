variable "bucket_id" {
  type = string
}

resource "aws_s3_bucket_policy" "p" {
  bucket = var.bucket_id
  policy = "{}"
}
