#!/bin/sh
set -eu

test "$(id -u)" = 1000
test "$(id -g)" = 1000
directory=$(mktemp -d)
trap 'rm -rf "$directory"' EXIT HUP INT TERM
printf 'int main(void) { return 0; }\n' > "$directory/smoke.c"
cc "$directory/smoke.c" -o "$directory/smoke"
"$directory/smoke"
