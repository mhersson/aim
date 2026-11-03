#!/bin/sh
# Fails on a wall-clock bound in a test that make test runs: time.Since,
# time.Until or time.Now().Sub in a _test.go file without the bench build tag.
# Such a test waits for the event or runs on an injected clock, and a
# real-time budget belongs behind the bench tag. A hang guard (time.After, a
# deadline from time.Now().Add) is a wait and passes. A line that measures
# without asserting ends with "// wall-clock: <why>".
set -u
export LC_ALL=C

bound='time\.(Since|Until)\(|time\.Now\(\)\.Sub\('
reason='// wall-clock: [^ ]'
bench='^//go:build(.*[^!A-Za-z0-9_])?bench([^A-Za-z0-9_]|$)'

# Prove each pattern matches what it must, and only that, before an empty
# result is trusted.
for s in 'if d := time.Since(start); d > time.Second {' 'd := time.Until(r)' 'gap := time.Now().Sub(at)'; do
	printf '%s\n' "$s" | grep -Eq "$bound" || { echo "scan-wall-clock: grep cannot match a bound in '$s'"; exit 1; }
done
for s in 'deadline := time.Now().Add(time.Second)' 'case <-time.After(hangGuard):' 'w := v.flashUntil.Sub(clk.Now())'; do
	printf '%s\n' "$s" | grep -Eq "$bound" && { echo "scan-wall-clock: '$s' matched as a bound"; exit 1; }
done
printf '%s\n' 'd := time.Since(t) // wall-clock: measured' | grep -Eq "$reason" || { echo "scan-wall-clock: grep cannot match a reason"; exit 1; }
for s in '//go:build bench' '//go:build linux && bench'; do
	printf '%s\n' "$s" | grep -Eq "$bench" || { echo "scan-wall-clock: grep cannot match the bench tag in '$s'"; exit 1; }
done
for s in '//go:build !bench' '//go:build benchmark'; do
	printf '%s\n' "$s" | grep -Eq "$bench" && { echo "scan-wall-clock: '$s' matched as the bench tag"; exit 1; }
done

list=$(mktemp "${TMPDIR:-/tmp}/scan-wall-clock.XXXXXX") || exit 1
probe=$(mktemp "${TMPDIR:-/tmp}/scan-wall-clock.XXXXXX") || { rm -f "$list"; exit 1; }
trap 'rm -f "$list" "$probe"' EXIT

# Prove the file pipeline finds a bound too: xargs and grep differ by platform.
printf '%s\n' '_ = time.Since(start)' > "$probe" || exit 1
found=$(printf '%s\0' "$probe" | xargs -0 grep -lE "$bound" /dev/null) || :
[ "$found" = "$probe" ] || { echo "scan-wall-clock: the file pipeline cannot find a bound"; exit 1; }

# From git, so a new file is caught before it is added; through a temp file,
# so a failing git fails the scan. /dev/null keeps grep off stdin.
git ls-files -z --cached --others --exclude-standard -- '*_test.go' > "$list" || exit 1
files=$(xargs -0 grep -lE "$bound" /dev/null < "$list") || :

report=$(printf '%s\n' "$files" | while IFS= read -r f; do
	[ -n "$f" ] || continue

	sed -n '1,/^package /p' "$f" | grep -Eq "$bench" && continue

	grep -nE "$bound" "$f" | grep -vE "$reason" | sed "s|^|$f:|"
done)

if [ -n "$report" ]; then
	echo "scan-wall-clock: wall-clock bounds in tests; wait for the event, inject a clock, or move the test behind the bench tag:"
	printf '%s\n' "$report"
	exit 1
fi
