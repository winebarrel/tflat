resource "aws_s3_bucket" "this" {
  count  = 3
  bucket = "b-${count.index}"
}
