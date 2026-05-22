variable "name" {
  type = string
}

resource "aws_s3_bucket" "this" {
  bucket = var.name
}

output "bucket_id" {
  value = aws_s3_bucket.this.id
}
