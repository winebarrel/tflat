module "m" {
  source = "./modules/m"
}

# Parent already owns `aws_s3_bucket.m_this`; flattening module "m" would
# rename its own `aws_s3_bucket.this` to the same address.
resource "aws_s3_bucket" "m_this" {
  bucket = "parent"
}
