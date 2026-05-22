module "web" {
  source = "./modules/web"
  count  = 3
  prefix = "b"
}
