resource "aws_iam_role" "this" {
  name = "m"
}

import {
  to = aws_iam_role.this
  id = "m"
}

moved {
  from = aws_iam_role.old
  to   = aws_iam_role.this
}

removed {
  from = aws_iam_role.gone
  lifecycle {
    destroy = false
  }
}
