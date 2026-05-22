module "rs" {
  source   = "./modules/rs"
  for_each = toset(["x", "y"])
  items    = ["a", "b"]
}
