module "outer" {
  source = "./modules/outer"
  name   = "hello"
}

resource "aws_iam_role_policy_attachment" "att" {
  role       = module.outer.role_arn
  policy_arn = "arn:..."
}
