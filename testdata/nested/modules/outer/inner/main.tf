variable "name" {
  type = string
}

resource "aws_iam_role" "role" {
  name = var.name
}

output "arn" {
  value = aws_iam_role.role.arn
}
