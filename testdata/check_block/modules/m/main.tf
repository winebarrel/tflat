data "aws_caller_identity" "current" {}

check "account" {
  assert {
    condition     = data.aws_caller_identity.current.account_id != ""
    error_message = "account id missing"
  }
}
