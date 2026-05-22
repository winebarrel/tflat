resource "aws_iam_role" "dep" {
  name = "d"
}

module "m" {
  source     = "./modules/m"
  name       = "x"
  depends_on = [aws_iam_role.dep]
  providers = {
    aws = aws
  }
}
