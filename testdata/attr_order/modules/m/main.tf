variable "name" {
  type = string
}

resource "aws_instance" "this" {
  count         = 1
  ami           = "ami-12345"
  instance_type = "t3.micro"
  tags = {
    Name = var.name
  }
  vpc_security_group_ids = []
}
