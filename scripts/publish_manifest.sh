#!/bin/bash
# Publishes the signed DIST/manifest.json as the update manifest of all
# versions, where updateSources in lib/install/update.go reads it:
#   - the release tagged channel, never marked as the latest release;
#   - the branch manifest-channel, holding only that file, for the CDN. A CDN
#     caches the files of a tag for good but refreshes a branch.
#
# Usage: GH_TOKEN=... scripts/publish_manifest.sh DIST
# Run it in a checkout that can push to origin.
set -euo pipefail

dist="$1"
tag="channel"

if ! gh release view "$tag" > /dev/null 2>&1; then
  gh release create "$tag" --title "Update manifest" \
    --notes "Holds the signed update manifest. Not a release to download." \
    --prerelease --latest=false
fi
gh release upload "$tag" "$dist/manifest.json" --clobber

# The branch holds one file with no history worth keeping.
blob=$(git hash-object -w "$dist/manifest.json")
tree=$(printf '100644 blob %s\tmanifest.json\n' "$blob" | git mktree)
commit=$(git -c user.name=djylb -c user.email=duan@d-jy.net commit-tree "$tree" -m "Update manifest")
git push --force origin "$commit:refs/heads/manifest-channel"
echo "Published $dist/manifest.json as the update manifest"
