# File-level comment describing this stack's purpose.
# Owned by the platform team.

module "m" {
  source = "./modules/m"
  name   = "x"
}

# Separator comment grouping unrelated resources below.
resource "aws_iam_role" "r" {
  name = "r"
}
