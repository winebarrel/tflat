variable "msg" {
  type = string
}

resource "null_resource" "n" {
  triggers = {
    when = "always"
  }

  provisioner "local-exec" {
    command = "echo ${var.msg}"
  }

  lifecycle {
    create_before_destroy = true
  }
}
