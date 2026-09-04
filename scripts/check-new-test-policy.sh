#!/usr/bin/env bash
set -euo pipefail

base=${1:-HEAD}
if ! git rev-parse --verify "${base}^{commit}" >/dev/null 2>&1; then
  echo "unknown test-policy base: $base" >&2
  exit 2
fi

# New diffs are checked below, then the final tree is required to contain no
# same-package or *_internal_test.go files in owned Go modules.

status=0
while IFS= read -r file; do
  [[ -z "$file" || ! -f "$file" || "$file" != *_test.go ]] && continue
  package=$(sed -n 's/^package[[:space:]]\+//p' "$file" | head -1)
  if [[ -n "$package" && "$package" != *_test ]]; then
    echo "$file: new tests must use an external *_test package" >&2
    status=1
  fi
done < <(git diff --diff-filter=A --name-only "$base" -- '*_test.go')

# Existing internal files may be edited while being migrated or maintained, but
# adding a new test entry point to one is forbidden.
while IFS= read -r file; do
  [[ -z "$file" || ! -f "$file" || "$file" != *_test.go ]] && continue
  package=$(sed -n 's/^package[[:space:]]\+//p' "$file" | head -1)
  [[ -z "$package" || "$package" == *_test ]] && continue
  if git diff --unified=0 "$base" -- "$file" | grep -Eq '^\+func (Test|Benchmark|Example)[A-Za-z0-9_]*\(' &&
    ! git diff --unified=0 "$base" -- "$file" | grep -Eq '^\+// ?testpolicy: grandfathered'; then
    echo "$file: new tests must use an external *_test package" >&2
    status=1
  fi
done < <(git diff --diff-filter=M --name-only "$base" -- '*_test.go')
while IFS=: read -r file line; do
  [[ -z "$file" || "$file" == examples/* || "$file" == */example_test.go ]] && continue
  echo "$file:$line: use t.Context() or b.Context() instead of context.Background()" >&2
  status=1
done < <(git diff --unified=0 "$base" -- '*_test.go' | awk '
  /^\+\+\+ b\// { file = substr($0, 7); next }
  /^@@ / {
    split($0, h, " "); split(h[3], r, ","); line = substr(r[1], 2) - 1; next
  }
  /^\+/ && !/^\+\+\+/ {
    line++
    if ($0 ~ /context\.Background\(\)/) print file ":" line
    next
  }
  /^ / { line++ }
')

while IFS= read -r file; do
  [[ "$file" == zarlcode/docs/images/workflow-demo-fixture/* ]] && continue
  [[ "$file" == zarlcode/tui/behavior_surface_export_test.go ]] && continue
  package=$(sed -n 's/^package[[:space:]]\+//p' "$file" | head -1)
  [[ -z "$package" || "$package" == *_test ]] && continue
  echo "$file: owned tests must use an external *_test package" >&2
  status=1
done < <(find tools examples zkit zarlcode swebench-eval -name '*_test.go' -type f)

if find tools examples zkit zarlcode swebench-eval -name '*_internal_test.go' -print -quit | grep -q .; then
  find tools examples zkit zarlcode swebench-eval -name '*_internal_test.go' -print >&2
  echo "owned *_internal_test.go files are forbidden" >&2
  status=1
fi

exit "$status"
