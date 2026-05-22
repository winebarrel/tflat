module "m" {
  source = "./modules/m"
}

# Same address that module "m" would produce for its inner data block.
data "aws_caller_identity" "m_current" {}
