#!/usr/bin/env bash
# 文档一致性检查：
#   1. 文档里不得引用已删除/更名的 API（历史问题：packet.NewWithWs）
#   2. examples/* 必须可编译
#   3. Go Example 必须可运行（输出与注释一致）
set -euo pipefail

cd "$(dirname "$0")/.."

fail=0

# 1) 陈旧 API / 常量黑名单。新增删除或更名时在此追加。
stale_patterns=(
  "packet.NewWithWs"
  "DefaultAppendDataTimeout"
  "DefaultSplitSize = 63KB"
  "bytesChan"
)

while IFS= read -r doc; do
  for pattern in "${stale_patterns[@]}"; do
    if grep -Fq -- "$pattern" "$doc"; then
      echo "ERROR: $doc references stale API/const: $pattern" >&2
      fail=1
    fi
  done
done < <(git ls-files '*.md' 2>/dev/null || find . -name '*.md' -not -path './.git/*')

# 2) 示例程序必须可编译
if ! go build ./examples/...; then
  echo "ERROR: examples failed to build" >&2
  fail=1
fi

# 3) Example 函数必须通过（编译 + 输出匹配）
if ! go test -run '^Example' -count=1 ./...; then
  echo "ERROR: Go Example tests failed" >&2
  fail=1
fi

if [ "$fail" -ne 0 ]; then
  exit 1
fi
echo "docs check passed"
