module "m" {
  source   = "./modules/m"
  count    = 2
  for_each = toset(["a"])
}
