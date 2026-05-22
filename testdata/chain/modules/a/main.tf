variable "name" {
  type = string
}

resource "aws_iam_role" "r" {
  name = var.name
}

output "id" {
  value = aws_iam_role.r.id
}
