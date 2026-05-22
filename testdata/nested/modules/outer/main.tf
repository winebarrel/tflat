variable "name" {
  type = string
}

module "inner" {
  source = "./inner"
  name   = var.name
}

output "role_arn" {
  value = module.inner.arn
}
