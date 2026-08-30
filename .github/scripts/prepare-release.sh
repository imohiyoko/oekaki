#!/usr/bin/env bash

set -euo pipefail

fail() {
  echo "::error::$*" >&2
  exit 1
}

valid_semver_tag() {
  local tag="$1" prerelease identifier
  [[ "$tag" =~ ^v(0|[1-9][0-9]*)[.](0|[1-9][0-9]*)[.](0|[1-9][0-9]*)(-[0-9A-Za-z-]+([.][0-9A-Za-z-]+)*)?$ ]] || return 1
  if [[ "$tag" == *-* ]]; then
    prerelease="${tag#*-}"
    IFS='.' read -r -a identifiers <<< "$prerelease"
    for identifier in "${identifiers[@]}"; do
      [[ "$identifier" =~ ^0[0-9]+$ ]] && return 1
    done
  fi
  return 0
}

: "${BUMP:?BUMP is required}"
: "${GITHUB_REF:?GITHUB_REF is required}"
: "${GITHUB_RUN_ID:?GITHUB_RUN_ID is required}"
: "${GITHUB_SHA:?GITHUB_SHA is required}"
: "${GITHUB_OUTPUT:?GITHUB_OUTPUT is required}"
: "${GITHUB_STEP_SUMMARY:?GITHUB_STEP_SUMMARY is required}"

PRE="${PRE:-}"

if [ "$GITHUB_REF" != "refs/heads/main" ]; then
  fail "release は main ブランチから実行してください（現在: ${GITHUB_REF}）"
fi

case "$BUMP" in
  major|minor|patch) ;;
  *) fail "unknown bump: $BUMP" ;;
esac

# SemVer pre-release identifiers are dot-separated, non-empty, and may only
# contain ASCII alphanumerics and hyphens. Numeric identifiers cannot have a
# leading zero.
if [ -n "$PRE" ]; then
  if [[ ! "$PRE" =~ ^[0-9A-Za-z-]+([.][0-9A-Za-z-]+)*$ ]]; then
    fail "prerelease は SemVer 識別子で指定してください（現在: ${PRE}）"
  fi
  IFS='.' read -r -a identifiers <<< "$PRE"
  for identifier in "${identifiers[@]}"; do
    if [[ "$identifier" =~ ^0[0-9]+$ ]]; then
      fail "prerelease の数値識別子に先頭ゼロは使えません（現在: ${identifier}）"
    fi
  done
fi

head_commit="$(git rev-parse HEAD)"
if [ "$head_commit" != "$GITHUB_SHA" ]; then
  fail "checkout HEAD (${head_commit}) が workflow commit (${GITHUB_SHA}) と一致しません"
fi

marker="[workflow-run:${GITHUB_RUN_ID}]"
next=""

# Reruns keep GITHUB_RUN_ID. The annotated tag records that ID, making the
# release version stable across attempts without mistaking an older tag on the
# same commit for the current release.
while IFS= read -r candidate; do
  subject="$(git for-each-ref --format='%(contents:subject)' "refs/tags/${candidate}")"
  if [[ "$subject" == *"$marker"* ]]; then
    if [ -n "$next" ]; then
      fail "workflow run ${GITHUB_RUN_ID} に複数の release tag があります"
    fi
    next="$candidate"
  fi
done < <(git tag -l 'v*')

if [ -n "$next" ]; then
  tag_commit="$(git rev-list -n 1 "refs/tags/${next}")"
  if [ "$tag_commit" != "$GITHUB_SHA" ]; then
    fail "retry tag ${next} は別の commit (${tag_commit}) を指しています"
  fi

  branch="release/${next}"
  remote_branch="$(git ls-remote --heads origin "refs/heads/${branch}" | awk 'NR == 1 { print $1 }')"
  remote_tag="$(git ls-remote --tags origin "refs/tags/${next}^{}" | awk 'NR == 1 { print $1 }')"
  if [ "$remote_branch" != "$GITHUB_SHA" ] || [ "$remote_tag" != "$GITHUB_SHA" ]; then
    fail "retry refs for ${next} are missing or do not point to ${GITHUB_SHA}"
  fi

  mode="reused"
else
  latest="v0.0.0"
  while IFS= read -r candidate; do
    # Tags created by this workflow have exactly this SemVer-shaped form.
    # Ignore unrelated v-prefixed tags instead of feeding them to arithmetic.
    if valid_semver_tag "$candidate"; then
      latest="$candidate"
      break
    fi
  done < <(git tag -l 'v*' --sort=-v:refname)

  base="${latest#v}"
  base="${base%%-*}"
  major="${base%%.*}"
  rest="${base#*.}"
  minor="${rest%%.*}"
  patch="${rest##*.}"

  case "$BUMP" in
    major) major=$((major + 1)); minor=0; patch=0 ;;
    minor) minor=$((minor + 1)); patch=0 ;;
    patch) patch=$((patch + 1)) ;;
  esac

  next="v${major}.${minor}.${patch}"
  if [ -n "$PRE" ]; then
    next="${next}-${PRE}"
  fi
  branch="release/${next}"

  if git show-ref --verify --quiet "refs/tags/${next}" ||
     git ls-remote --exit-code --tags origin "refs/tags/${next}" >/dev/null 2>&1; then
    fail "tag ${next} は既に存在します"
  fi
  if git show-ref --verify --quiet "refs/heads/${branch}" ||
     git ls-remote --exit-code --heads origin "refs/heads/${branch}" >/dev/null 2>&1; then
    fail "branch ${branch} は既に存在します"
  fi

  git config user.name "github-actions[bot]"
  git config user.email "41898282+github-actions[bot]@users.noreply.github.com"
  git branch "$branch" "$GITHUB_SHA"
  git tag -a "$next" "$GITHUB_SHA" -m "release ${next} ${marker}"

  # Keep the immutable tag and its matching release branch all-or-nothing.
  git push --atomic origin \
    "refs/heads/${branch}:refs/heads/${branch}" \
    "refs/tags/${next}:refs/tags/${next}"
  mode="created"
fi

{
  echo "tag=${next}"
  echo "branch=${branch}"
  echo "mode=${mode}"
} >> "$GITHUB_OUTPUT"

echo "release ${next}: ${mode} (branch: ${branch})"
echo "## release ${next} (${mode}, branch ${branch})" >> "$GITHUB_STEP_SUMMARY"
