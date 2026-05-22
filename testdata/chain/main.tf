module "a" {
  source = "./modules/a"
  name   = "n"
}

module "b" {
  source     = "./modules/b"
  upstream_a = module.a.id
}

resource "aws_iam_role_policy_attachment" "att" {
  role       = module.b.derived_a
  policy_arn = "arn:..."
}
