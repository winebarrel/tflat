variable "ports" {
  type = list(number)
}

resource "aws_security_group" "sg" {
  name = "sg"

  dynamic "ingress" {
    for_each = toset(var.ports)
    content {
      from_port = ingress.value
      to_port   = ingress.value
      protocol  = "tcp"
    }
  }
}
