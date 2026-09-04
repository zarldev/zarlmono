#!/usr/bin/env bash
set -euo pipefail

readonly anthropic_module=github.com/anthropics/anthropic-sdk-go
readonly anthropic_version=v1.68.0
readonly jsonschema_module=github.com/invopop/jsonschema
readonly jsonschema_version=v0.14.0
readonly modules=(examples swebench-eval zarlcode zkit)

status=0
for module in "${modules[@]}"; do
  anthropic=$(cd "$module" && GOWORK=off go list -mod=readonly -m -f '{{.Version}}' "$anthropic_module")
  jsonschema=$(cd "$module" && GOWORK=off go list -mod=readonly -m -f '{{.Version}}' "$jsonschema_module")

  if [[ "$anthropic" != "$anthropic_version" || "$jsonschema" != "$jsonschema_version" ]]; then
    printf '%s: incompatible Anthropic/JSON Schema pair: %s %s, %s %s\n' \
      "$module" "$anthropic_module" "$anthropic" "$jsonschema_module" "$jsonschema" >&2
    status=1
  fi
done

if ((status == 0)); then
  printf 'Anthropic/JSON Schema pair verified in: %s\n' "${modules[*]}"
fi

exit "$status"
