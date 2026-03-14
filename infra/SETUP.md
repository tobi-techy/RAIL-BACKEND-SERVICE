# AWS Secrets & Environment Setup

All secrets are stored in **SSM Parameter Store** as `SecureString`.
The ECS task execution role has read access to `/rail/staging/*`.

## 1. Store secrets (run once)

```bash
ENV=staging
REGION=us-east-1

aws ssm put-parameter --region $REGION --type SecureString \
  --name /rail/$ENV/jwt_secret        --value "YOUR_JWT_SECRET"

aws ssm put-parameter --region $REGION --type SecureString \
  --name /rail/$ENV/encryption_key    --value "YOUR_32_BYTE_KEY"

aws ssm put-parameter --region $REGION --type SecureString \
  --name /rail/$ENV/bridge_api_key    --value "YOUR_BRIDGE_KEY"

aws ssm put-parameter --region $REGION --type SecureString \
  --name /rail/$ENV/alpaca_api_key    --value "YOUR_ALPACA_KEY"

aws ssm put-parameter --region $REGION --type SecureString \
  --name /rail/$ENV/alpaca_api_secret --value "YOUR_ALPACA_SECRET"

aws ssm put-parameter --region $REGION --type SecureString \
  --name /rail/$ENV/sendgrid_api_key  --value "YOUR_SENDGRID_KEY"
```

## 2. GitHub Actions secrets (set in repo Settings → Secrets)

| Secret | Value |
|--------|-------|
| `AWS_DEPLOY_ROLE_ARN` | ARN of the IAM role GitHub OIDC assumes (see step 3) |

## 3. GitHub OIDC trust (run once — no long-lived AWS keys in GitHub)

```bash
# Create the OIDC provider for GitHub Actions
aws iam create-open-id-connect-provider \
  --url https://token.actions.githubusercontent.com \
  --client-id-list sts.amazonaws.com \
  --thumbprint-list 6938fd4d98bab03faadb97b34396831e3780aea1

# Create deploy role — replace ORG/REPO with your GitHub org/repo
aws iam create-role \
  --role-name rail-github-deploy \
  --assume-role-policy-document '{
    "Version": "2012-10-17",
    "Statement": [{
      "Effect": "Allow",
      "Principal": { "Federated": "arn:aws:iam::ACCOUNT_ID:oidc-provider/token.actions.githubusercontent.com" },
      "Action": "sts:AssumeRoleWithWebIdentity",
      "Condition": {
        "StringLike": {
          "token.actions.githubusercontent.com:sub": "repo:ORG/REPO:ref:refs/heads/staging"
        }
      }
    }]
  }'

# Attach minimum permissions
aws iam attach-role-policy \
  --role-name rail-github-deploy \
  --policy-arn arn:aws:iam::aws:policy/AmazonEC2ContainerRegistryPowerUser

aws iam attach-role-policy \
  --role-name rail-github-deploy \
  --policy-arn arn:aws:iam::aws:policy/AmazonECS_FullAccess
```

## 4. First deploy

```bash
# Bootstrap Terraform state bucket (once)
aws s3 mb s3://rail-terraform-state --region us-east-1
aws s3api put-bucket-versioning \
  --bucket rail-terraform-state \
  --versioning-configuration Status=Enabled

# Apply infrastructure
cd infra/terraform
terraform init
TF_VAR_db_password="YOUR_DB_PASSWORD" terraform apply

# Note the outputs — add alb_dns as a CNAME in your DNS
terraform output alb_dns
```

## 5. Run database migrations

After first deploy, run migrations once via ECS exec or a one-off task:

```bash
aws ecs run-task \
  --cluster rail-staging \
  --task-definition rail-staging \
  --launch-type FARGATE \
  --network-configuration "awsvpcConfiguration={subnets=[SUBNET_ID],securityGroups=[SG_ID]}" \
  --overrides '{"containerOverrides":[{"name":"rail-backend","command":["migrate"]}]}'
```

## Cost estimate (free tier, first 12 months)

| Resource | Free tier | Cost after |
|----------|-----------|------------|
| ECS Fargate (0.25 vCPU / 512MB) | Not free — ~$9/month | ~$9/month |
| RDS db.t3.micro | 750h/month free | ~$15/month |
| ElastiCache cache.t3.micro | 750h/month free | ~$12/month |
| ALB | 750h + 15 LCU free | ~$16/month |
| ECR | 500MB free | ~$0 |
| Data transfer | 1GB free | ~$1/month |

**Total during free tier: ~$9/month (only Fargate isn't free)**
**Total after free tier: ~$53/month**

> Fargate has no free tier. To get to $0, swap ECS Fargate for a single EC2 t2.micro
> (free tier) running the container via Docker. See `infra/ec2-alternative/` if needed.
