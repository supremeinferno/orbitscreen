#!/bin/sh
# Downloads the orbit data this site published last time into a folder
# (default data), for when CelesTrak will not serve the workflow's request:
# it answers 403 to GitHub's servers at times. The site then rebuilds with
# data that is as old as its last successful build, which is better than not
# rebuilding at all; each object's panel shows the age of its elements.
#
#   GITHUB_REPOSITORY=owner/name site/reuse-data.sh [folder]
set -eu
dir=${1:-data}
repo=${GITHUB_REPOSITORY:?GITHUB_REPOSITORY is not set (owner/name)}
base="https://${repo%%/*}.github.io/${repo#*/}/data"

mkdir -p "$dir"
list=$(mktemp)
curl -fsSL --retry 3 "$base/files.txt" -o "$list"
[ -s "$list" ] || { echo "the site lists no data files" >&2; exit 1; }
while IFS= read -r f; do
  case "$f" in *.csv) ;; *) continue ;; esac
  curl -fsSL --retry 3 "$base/$f" -o "$dir/$f"
done <"$list"
rm -f "$list"
ls "$dir"/*.csv >/dev/null
echo "reused $(ls "$dir"/*.csv | wc -l | tr -d ' ') data files from $base"
