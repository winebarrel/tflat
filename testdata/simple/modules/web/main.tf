variable "bucket" {
  type = string
}

resource "aws_s3_bucket" "this" {
  bucket = var.bucket
}

output "bucket_id" {
  value = aws_s3_bucket.this.id
}
