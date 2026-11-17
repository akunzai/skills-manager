#!/usr/bin/env bash
# Runs the module's tests with the slow packages split across concurrent
# processes, and prints how long building and each shard took.
#
#   test-shards.sh SHARDS PACKAGE...
#
# Each PACKAGE is built once with `go test -c`, its top-level tests are dealt
# round-robin into SHARDS groups, and every group runs as its own process of
# that binary. The other packages run in one more `go test` meanwhile. A slow
# package's tests are mostly sequential and bound by spawning git, which is
# what makes the Windows job slow, so the idle cores take the other shards.
# Each process's TestMain isolates its own state, so shards share nothing.
# Each background job records its own exit code, since errexit would end the
# subshell before it could.
set -euo pipefail

shards=${1:?usage: test-shards.sh SHARDS PACKAGE...}
shift
[ "$#" -gt 0 ] || { echo "usage: test-shards.sh SHARDS PACKAGE..." >&2; exit 2; }

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
exe=
case "$(go env GOOS)" in windows) exe=.exe ;; esac

now() { date +%s; }
# path converts what go list prints to a path this shell can cd into.
path() { if command -v cygpath >/dev/null; then cygpath -u "$1"; else printf '%s\n' "$1"; fi; }

slow=()
for pkg in "$@"; do slow+=("$(go list "$pkg")"); done
others=()
while IFS= read -r pkg; do
	case " ${slow[*]} " in *" $pkg "*) ;; *) others+=("$pkg") ;; esac
done < <(go list ./...)

names=() pids=()
start=$(now)
if [ "${#others[@]}" -gt 0 ]; then
	(code=0; go test "${others[@]}" >"$work/others.log" 2>&1 || code=$?; echo "$code" >"$work/others.code"; echo "$(($(now) - start))" >"$work/others.time") &
	names+=(others) pids+=($!)
fi

# Every binary is built before any shard starts: a shard running while the
# next package links takes the CPU that link needs.
for pkg in "${slow[@]}"; do
	built=$(now)
	go test -c -o "$work/$(basename "$pkg").test$exe" "$pkg"
	echo "built $pkg in $(($(now) - built))s"
done

for pkg in "${slow[@]}"; do
	id=$(basename "$pkg")
	bin="$work/$id.test$exe"
	mapfile -t tests < <("$bin" -test.list '.' | grep -E '^(Test|Fuzz|Example)')
	dir=$(path "$(go list -f '{{.Dir}}' "$pkg")")
	for ((i = 0; i < shards; i++)); do
		group=()
		for ((j = i; j < ${#tests[@]}; j += shards)); do group+=("${tests[j]}"); done
		[ "${#group[@]}" -gt 0 ] || continue
		pattern="^($(IFS='|'; echo "${group[*]}"))\$"
		shard="$id-$i"
		began=$(now)
		(code=0; cd "$dir" && "$bin" -test.timeout=10m -test.run "$pattern" >"$work/$shard.log" 2>&1 || code=$?; echo "$code" >"$work/$shard.code"; echo "$(($(now) - began))" >"$work/$shard.time") &
		names+=("$shard") pids+=($!)
	done
done

wait "${pids[@]}" || true
failed=0
for name in "${names[@]}"; do
	code=$(cat "$work/$name.code")
	printf '%-12s %4ss  exit %s\n' "$name" "$(cat "$work/$name.time")" "$code"
	if [ "$code" != 0 ]; then
		failed=1
		cat "$work/$name.log"
	fi
done
echo "all tests in $(($(now) - start))s"
exit "$failed"
