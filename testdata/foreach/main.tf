module "web" {
  source   = "./modules/web"
  for_each = toset(["a", "b"])
  bucket   = each.value
}
