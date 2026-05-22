module "a" {
  source = "./modules/a"
  name   = "bucket-a"
}

module "b" {
  source    = "./modules/b"
  bucket_id = module.a.bucket_id
}
