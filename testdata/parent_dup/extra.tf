# Duplicate of aws_s3_bucket.dup in main.tf. Invalid Terraform that
# tflat surfaces up front rather than silently flattening through.
resource "aws_s3_bucket" "dup" {
  bucket = "second"
}
