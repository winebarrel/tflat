resource "aws_s3_bucket" "dup" {
  bucket = "first"
}

resource "aws_s3_bucket" "dup" {
  bucket = "second"
}
