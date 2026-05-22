# A syntactically-valid `module {}` block with zero labels at the parent
# level. Real Terraform would reject it, but tflat must not panic on it.
module {}

module "m" {
  source = "./modules/m"
}
