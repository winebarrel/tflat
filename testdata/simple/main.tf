module "web" {
  source = "./modules/web"
  bucket = "my-bucket"
}

resource "aws_s3_bucket_policy" "p" {
  bucket = module.web.bucket_id
  policy = "{}"
}
