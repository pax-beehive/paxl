#!/usr/bin/env bash
set -euo pipefail

gobco="${1:-gobco}"
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

total_covered=0
total_branches=0
package_list="$(mktemp)"
go_list_stderr="$(mktemp)"
trap 'rm -f "$package_list" "$go_list_stderr"' EXIT

printf 'Running gobco branch coverage report.\n\n'

(
	cd "$repo_root"
	go list -f '{{.ImportPath}}{{"\t"}}{{.Dir}}' ./... > "$package_list" 2> "$go_list_stderr"
)

if [[ -s "$go_list_stderr" ]]; then
	grep -v '^go: writing stat cache:' "$go_list_stderr" >&2 || true
fi

while IFS=$'\t' read -r import_path dir; do
	if [[ -z "${import_path}" || -z "${dir}" ]]; then
		continue
	fi

	rel_dir="${dir#"$repo_root"/}"
	printf '## %s\n' "$import_path"

	output="$(
		cd "$dir"
		"$gobco" -branch .
	)"
	printf '%s\n\n' "$output"

	coverage_line="$(printf '%s\n' "$output" | awk '/^Branch coverage:/ { print $3; exit }')"
	if [[ -z "$coverage_line" ]]; then
		printf 'No branch coverage total found for %s (%s).\n\n' "$import_path" "$rel_dir"
		continue
	fi

	covered="${coverage_line%%/*}"
	branches="${coverage_line##*/}"
	total_covered=$((total_covered + covered))
	total_branches=$((total_branches + branches))
done < "$package_list"

if [[ "$total_branches" -eq 0 ]]; then
	printf 'Branch coverage total: 0/0 (n/a)\n'
	exit 0
fi

awk -v covered="$total_covered" -v total="$total_branches" 'BEGIN {
	printf "Branch coverage total: %d/%d (%.1f%%)\n", covered, total, covered * 100 / total
}'
