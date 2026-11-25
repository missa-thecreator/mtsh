#!/bin/sh
# Calcula a próxima versão a partir dos commits desde a última tag vX.Y.Z,
# seguindo Conventional Commits. Imprime a versão (ex: v0.3.0), ou nada se
# não houver o que lançar (nenhum commit novo, ou só documentação).
#
#   feat: ...                      → sobe o do meio   (v0.2.4 → v0.3.0)
#   fix: / chore: / qualquer outro → sobe o último    (v0.2.4 → v0.2.5)
#   feat!: ... ou "BREAKING CHANGE" → sobe o primeiro (v1.2.3 → v2.0.0);
#                                     antes da 1.0, sobe o do meio
set -eu

last="$(git describe --tags --abbrev=0 --match 'v[0-9]*.[0-9]*.[0-9]*' 2>/dev/null || true)"
if [ -n "$last" ]; then
	range="$last..HEAD"
else
	last="v0.0.0"
	range="HEAD"
fi

# nada novo desde a última versão
[ -n "$(git rev-list "$range" 2>/dev/null)" ] || exit 0

# só documentação? (README, outros .md, docs/) então não lança versão
code_changed="$(git diff --name-only "$([ "$range" = HEAD ] && git hash-object -t tree /dev/null || echo "$last")" HEAD |
	grep -v -E '(^|/)[^/]*\.md$|^docs/|^LICENSE$' || true)"
[ -n "$code_changed" ] || exit 0

msgs="$(git log --format='%s%n%b' "$range")"

v="${last#v}"
major="${v%%.*}"
rest="${v#*.}"
minor="${rest%%.*}"
patch="${rest#*.}"

if printf '%s\n' "$msgs" | grep -q -E '^[a-z]+(\([^)]*\))?!:|BREAKING[ -]CHANGE'; then
	if [ "$major" -eq 0 ]; then
		minor=$((minor + 1)); patch=0
	else
		major=$((major + 1)); minor=0; patch=0
	fi
elif printf '%s\n' "$msgs" | grep -q -E '^feat(\([^)]*\))?:'; then
	minor=$((minor + 1)); patch=0
else
	patch=$((patch + 1))
fi

echo "v$major.$minor.$patch"
