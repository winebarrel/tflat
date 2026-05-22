module "inner" {
  source = "./inner"
  count  = 2
  name   = "n-${count.index}"
}
