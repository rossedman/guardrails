#!/usr/bin/env bash
# Set the release version everywhere it is pinned: the plugin manifest (the
# launcher downloads the binary matching it), the install snippets in the
# README and docs, and the changelog's Unreleased heading.
# Usage: scripts/bump-version.sh 1.2.3
set -euo pipefail

new=${1:-}
if ! [[ $new =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "usage: $0 X.Y.Z" >&2
  exit 2
fi
cd "$(dirname "$0")/.."

manifest=.claude-plugin/plugin.json
old=$(jq -r .version "$manifest")
# Edit the one line, so the manifest's formatting is kept.
sed -i.bak -E "s/(\"version\": *\")[^\"]*\"/\1${new}\"/" "$manifest"
rm -f "$manifest.bak"
if [ "$(jq -r .version "$manifest")" != "$new" ]; then
  echo "could not set the version in $manifest" >&2
  exit 1
fi

# Pinned references: @vX.Y.Z (Action, go install), rev: vX.Y.Z (pre-commit)
# and the Action's example version input.
old_re=${old//./\\.}
for f in README.md docs/*.md; do
  sed -i.bak -E \
    -e "s/@v${old_re}\b/@v${new}/g" \
    -e "s/rev: v${old_re}\b/rev: v${new}/g" \
    -e "s/e\.g\. \`${old_re}\`/e.g. \`${new}\`/g" \
    "$f"
  rm -f "$f.bak"
done

if grep -q '^## Unreleased' CHANGELOG.md; then
  sed -i.bak "s/^## Unreleased$/## ${new}/" CHANGELOG.md
  rm -f CHANGELOG.md.bak
fi

echo "version ${old} -> ${new}"
echo "next: merge to main, then: git tag v${new} && git push origin v${new}"
