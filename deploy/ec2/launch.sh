#!/usr/bin/env bash
# Launch the EC2 instance that hosts the Lemon Search API (ADR-0007). Creates a
# key pair + security group, resolves the latest Ubuntu 24.04 x86-64 AMI, and
# starts a c7i.xlarge in us-east-1. It does NOT provision the box — SSH in and
# run setup.sh after (the script prints the exact command).
#
# Safety: creates billable resources, so it refuses to run until you opt in:
#   LAUNCH_CONFIRM=yes deploy/ec2/launch.sh
# Without that, it prints the plan and exits.
#
# Requires: aws CLI authenticated (aws sts get-caller-identity must work).
set -euo pipefail

AWS_REGION="${AWS_REGION:-us-east-1}"
INSTANCE_TYPE="${INSTANCE_TYPE:-c7i.xlarge}"
KEY_NAME="${KEY_NAME:-lemon-api}"
SG_NAME="${SG_NAME:-lemon-api-sg}"
VOLUME_GB="${VOLUME_GB:-20}"
KEY_DIR="${KEY_DIR:-$HOME/.ssh}"
KEY_FILE="$KEY_DIR/${KEY_NAME}.pem"
NAME_TAG="${NAME_TAG:-lemon-api}"
export AWS_PAGER=""

aws sts get-caller-identity >/dev/null 2>&1 || { echo "aws not authenticated — run 'aws configure sso' or export AWS_* creds" >&2; exit 1; }

# SSH ingress is locked to your current public IP; the API port is public so the
# web app / graders can reach it. Override SSH_CIDR to widen/narrow.
MY_IP="$(curl -fsS https://checkip.amazonaws.com | tr -d '[:space:]')"
SSH_CIDR="${SSH_CIDR:-${MY_IP}/32}"

AMI_ID="$(aws ssm get-parameters --region "$AWS_REGION" \
  --names /aws/service/canonical/ubuntu/server/24.04/stable/current/amd64/hvm/ebs-gp3/ami-id \
  --query 'Parameters[0].Value' --output text)"

echo "── launch plan ──────────────────────────────────────────"
echo "  region        $AWS_REGION"
echo "  instance      $INSTANCE_TYPE  (Ubuntu 24.04, $AMI_ID)"
echo "  root volume   ${VOLUME_GB}GB gp3"
echo "  key pair      $KEY_NAME  → $KEY_FILE"
echo "  security grp  $SG_NAME   (22 from ${SSH_CIDR}, 8080 from 0.0.0.0/0)"
echo "  est. cost     ~\$0.18/hr on-demand (tear down when done: teardown.sh)"
echo "─────────────────────────────────────────────────────────"
if [ "${LAUNCH_CONFIRM:-}" != "yes" ]; then
  echo "DRY RUN. Re-run with LAUNCH_CONFIRM=yes to create these resources."
  exit 0
fi

echo "==> key pair"
if ! aws ec2 describe-key-pairs --region "$AWS_REGION" --key-names "$KEY_NAME" >/dev/null 2>&1; then
  mkdir -p "$KEY_DIR"
  aws ec2 create-key-pair --region "$AWS_REGION" --key-name "$KEY_NAME" \
    --query 'KeyMaterial' --output text > "$KEY_FILE"
  chmod 600 "$KEY_FILE"
  echo "   created $KEY_FILE"
else
  echo "   reusing existing key pair $KEY_NAME (need $KEY_FILE locally)"
fi

echo "==> security group"
SG_ID="$(aws ec2 describe-security-groups --region "$AWS_REGION" \
  --filters "Name=group-name,Values=$SG_NAME" \
  --query 'SecurityGroups[0].GroupId' --output text 2>/dev/null)"
if [ "$SG_ID" = "None" ] || [ -z "$SG_ID" ]; then
  SG_ID="$(aws ec2 create-security-group --region "$AWS_REGION" \
    --group-name "$SG_NAME" --description "Lemon Search API" \
    --query 'GroupId' --output text)"
  aws ec2 authorize-security-group-ingress --region "$AWS_REGION" --group-id "$SG_ID" \
    --ip-permissions "IpProtocol=tcp,FromPort=22,ToPort=22,IpRanges=[{CidrIp=${SSH_CIDR}}]" \
    "IpProtocol=tcp,FromPort=8080,ToPort=8080,IpRanges=[{CidrIp=0.0.0.0/0}]" >/dev/null
  echo "   created $SG_ID"
else
  echo "   reusing $SG_ID"
fi

echo "==> run-instances"
IID="$(aws ec2 run-instances --region "$AWS_REGION" \
  --image-id "$AMI_ID" --instance-type "$INSTANCE_TYPE" \
  --key-name "$KEY_NAME" --security-group-ids "$SG_ID" \
  --block-device-mappings "DeviceName=/dev/sda1,Ebs={VolumeSize=${VOLUME_GB},VolumeType=gp3}" \
  --tag-specifications "ResourceType=instance,Tags=[{Key=Name,Value=${NAME_TAG}}]" \
  --query 'Instances[0].InstanceId' --output text)"
echo "   $IID — waiting for running…"
aws ec2 wait instance-running --region "$AWS_REGION" --instance-ids "$IID"

HOST="$(aws ec2 describe-instances --region "$AWS_REGION" --instance-ids "$IID" \
  --query 'Reservations[0].Instances[0].PublicDnsName' --output text)"

echo "─────────────────────────────────────────────────────────"
echo "  instance   $IID"
echo "  host       $HOST"
echo "  ssh        ssh -i $KEY_FILE ubuntu@$HOST"
echo "  next       provision the box:"
echo "    ssh -i $KEY_FILE ubuntu@$HOST 'git clone https://github.com/danielreales00/lemon-search.git && sudo REPO_REF=main bash lemon-search/deploy/ec2/setup.sh'"
echo "  teardown   INSTANCE_ID=$IID deploy/ec2/teardown.sh"
echo "─────────────────────────────────────────────────────────"
