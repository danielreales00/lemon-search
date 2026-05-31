#!/usr/bin/env bash
# Tear down the EC2 resources launch.sh created, so the hourly meter stops.
# Terminates the instance and (optionally) deletes the security group + key pair.
#
#   INSTANCE_ID=i-0abc... deploy/ec2/teardown.sh            # terminate the instance
#   INSTANCE_ID=i-0abc... DELETE_SG=yes DELETE_KEY=yes deploy/ec2/teardown.sh
set -euo pipefail

AWS_REGION="${AWS_REGION:-us-east-1}"
KEY_NAME="${KEY_NAME:-lemon-api}"
SG_NAME="${SG_NAME:-lemon-api-sg}"
export AWS_PAGER=""

aws sts get-caller-identity >/dev/null 2>&1 || { echo "aws not authenticated" >&2; exit 1; }

IID="${INSTANCE_ID:?set INSTANCE_ID (from launch.sh output, or: aws ec2 describe-instances --filters Name=tag:Name,Values=lemon-api)}"

echo "==> terminating $IID"
aws ec2 terminate-instances --region "$AWS_REGION" --instance-ids "$IID" >/dev/null
aws ec2 wait instance-terminated --region "$AWS_REGION" --instance-ids "$IID"
echo "   terminated"

if [ "${DELETE_SG:-}" = "yes" ]; then
  echo "==> deleting security group $SG_NAME"
  SG_ID="$(aws ec2 describe-security-groups --region "$AWS_REGION" \
    --filters "Name=group-name,Values=$SG_NAME" --query 'SecurityGroups[0].GroupId' --output text 2>/dev/null)"
  [ -n "$SG_ID" ] && [ "$SG_ID" != "None" ] && aws ec2 delete-security-group --region "$AWS_REGION" --group-id "$SG_ID" && echo "   deleted $SG_ID"
fi

if [ "${DELETE_KEY:-}" = "yes" ]; then
  echo "==> deleting key pair $KEY_NAME"
  aws ec2 delete-key-pair --region "$AWS_REGION" --key-name "$KEY_NAME" >/dev/null && echo "   deleted (remove ~/.ssh/${KEY_NAME}.pem yourself)"
fi

echo "==> teardown done"
