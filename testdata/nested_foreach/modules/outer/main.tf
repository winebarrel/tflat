module "inner" {
  source   = "./inner"
  for_each = toset(["x", "y"])
  name     = each.value
}
