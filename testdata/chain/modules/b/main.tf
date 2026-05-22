variable "upstream_a" {
  type = string
}

# b's output is *purely* derived from b's variable, which itself was passed
# module.a.id from the parent. We never read aws_iam_role.r directly here.
output "derived_a" {
  value = var.upstream_a
}
