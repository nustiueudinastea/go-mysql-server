#!/bin/sh
# Run on an arm64 host with amd64 execution support (Rosetta on macOS).
set -eu
cd "$(dirname "$0")/.."
plan_dir=${1:-$(mktemp -d)}
mkdir -p "$plan_dir"
plan_dir=$(cd "$plan_dir" && pwd)
GMS_RULE_PLAN_FILE="$plan_dir/arm64.txt" GOARCH=arm64 go test -p 2 -count=1 -tags=gms_pure_go ./ -run '^TestRuleBasedPlanCorpus$'
GMS_RULE_PLAN_FILE="$plan_dir/amd64.txt" GOARCH=amd64 go test -p 2 -count=1 -tags=gms_pure_go ./ -run '^TestRuleBasedPlanCorpus$'
cmp "$plan_dir/arm64.txt" "$plan_dir/amd64.txt"
echo "50 rule-based plans are byte-identical: $plan_dir/arm64.txt and $plan_dir/amd64.txt"
