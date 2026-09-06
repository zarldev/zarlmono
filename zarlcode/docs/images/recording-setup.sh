#!/usr/bin/env bash

# This file is sourced by the VHS tapes from the repository root. It keeps build and
# fixture setup out of recorded frames while leaving cleanup available to the tape.
recording_cleanup() {
	trap - EXIT HUP INT TERM
	if [[ -n "${REC_PID:-}" ]]; then
		kill "$REC_PID" 2>/dev/null || true
		wait "$REC_PID" 2>/dev/null || true
	fi
	if [[ -n "${REC_ROOT:-}" ]]; then
		rm -rf -- "$REC_ROOT"
	fi
}

trap recording_cleanup EXIT
trap 'exit 1' HUP INT TERM

REC_REPO_ROOT=$PWD
REC_GOCACHE=$(go env GOCACHE) || return 1
REC_GOMODCACHE=$(go env GOMODCACHE) || return 1
REC_ROOT=$(mktemp -d) || return 1
export REC_ROOT
export HOME="$REC_ROOT/home"
export XDG_CONFIG_HOME="$REC_ROOT/config"
export XDG_DATA_HOME="$REC_ROOT/data"
export XDG_CACHE_HOME="$REC_ROOT/cache"
export GOCACHE="$REC_GOCACHE"
export GOMODCACHE="$REC_GOMODCACHE"

mkdir -p "$HOME" "$REC_ROOT/greeting-demo" || return 1
cp -R "$REC_REPO_ROOT/zarlcode/docs/images/workflow-demo-fixture/." "$REC_ROOT/greeting-demo" || return 1
go build -o "$REC_ROOT/zarlcode" "$REC_REPO_ROOT/zarlcode/cmd" || return 1
GOWORK=off go build -C "$REC_REPO_ROOT/zarlcode/docs/images/recording-fixture" -o "$REC_ROOT/recording-fixture" . || return 1

REC_READY="$REC_ROOT/fixture.addr"
"$REC_ROOT/recording-fixture" -addr 127.0.0.1:8081 -ready-file "$REC_READY" -owner-pid "$PPID" >"$REC_ROOT/fixture.log" 2>&1 &
REC_PID=$!
export REC_PID

for _ in $(seq 1 100); do
	if [[ -s "$REC_READY" ]]; then
		break
	fi
	if ! kill -0 "$REC_PID" 2>/dev/null; then
		cat "$REC_ROOT/fixture.log" >&2
		return 1
	fi
	sleep 0.1
done

if [[ ! -s "$REC_READY" ]]; then
	printf 'recording fixture did not become ready\n' >&2
	return 1
fi
if ! kill -0 "$REC_PID" 2>/dev/null; then
	cat "$REC_ROOT/fixture.log" >&2
	return 1
fi

REC_ADDR=$(cat "$REC_READY") || return 1
export LLAMACPP_BASE_URL="http://$REC_ADDR/v1"
curl -fsS "http://$REC_ADDR/health" >/dev/null || return 1
cd "$REC_ROOT/greeting-demo" || return 1
