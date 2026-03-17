# EU West environment — eu-west-1
# WARNING: This requires a separate Terraform workspace and state file.
# Do NOT apply this against an existing us-east-1 workspace — it will destroy all resources.
#
# Setup for new region:
#   terraform workspace new eu-west-1
#   terraform apply -var-file=eu-west-1.tfvars
aws_region = "eu-west-1"
