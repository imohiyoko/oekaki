#!/usr/bin/env bash

set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
prepare="${script_dir}/prepare-release.sh"
test_root="$(mktemp -d)"
trap 'rm -rf "$test_root"' EXIT

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

assert_output() {
  local file="$1" expected="$2"
  grep -Fxq "$expected" "$file" || fail "missing output: ${expected}"
}

git init --bare "$test_root/origin.git" >/dev/null
git init -b main "$test_root/work" >/dev/null
git -C "$test_root/work" config user.name test
git -C "$test_root/work" config user.email test@example.com
git -C "$test_root/work" commit --allow-empty -m initial >/dev/null
git -C "$test_root/work" tag -a v1.2.3 -m v1.2.3
git -C "$test_root/work" tag -a v9.0.0-01 -m 'invalid SemVer must be ignored'
git -C "$test_root/work" remote add origin "$test_root/origin.git"
git -C "$test_root/work" push origin main v1.2.3 v9.0.0-01 >/dev/null

sha="$(git -C "$test_root/work" rev-parse HEAD)"
output="$test_root/output"
summary="$test_root/summary"

run_prepare() {
  (
    cd "$test_root/work"
    BUMP=minor \
    PRE=rc.1 \
    GITHUB_REF=refs/heads/main \
    GITHUB_RUN_ID=12345 \
    GITHUB_SHA="$sha" \
    GITHUB_OUTPUT="$output" \
    GITHUB_STEP_SUMMARY="$summary" \
      "$prepare"
  )
}

run_prepare >/dev/null
assert_output "$output" 'tag=v1.3.0-rc.1'
assert_output "$output" 'branch=release/v1.3.0-rc.1'
assert_output "$output" 'mode=created'
[ "$(git -C "$test_root/work" ls-remote --heads origin refs/heads/release/v1.3.0-rc.1 | awk '{print $1}')" = "$sha" ] || fail 'release branch commit mismatch'
[ "$(git -C "$test_root/work" ls-remote --tags origin 'refs/tags/v1.3.0-rc.1^{}' | awk '{print $1}')" = "$sha" ] || fail 'release tag commit mismatch'

# A rerun of the same workflow run reuses its immutable refs.
: > "$output"
run_prepare >/dev/null
assert_output "$output" 'tag=v1.3.0-rc.1'
assert_output "$output" 'mode=reused'
[ -z "$(git -C "$test_root/work" tag -l 'v1.3.1*')" ] || fail 'rerun allocated a new version'

for invalid in 'rc..1' '01' '.rc' 'rc.'; do
  if (
    cd "$test_root/work"
    BUMP=patch PRE="$invalid" GITHUB_REF=refs/heads/main \
    GITHUB_RUN_ID=67890 GITHUB_SHA="$sha" \
    GITHUB_OUTPUT="$output" GITHUB_STEP_SUMMARY="$summary" \
      "$prepare" >/dev/null 2>&1
  ); then
    fail "invalid prerelease was accepted: ${invalid}"
  fi
done

echo 'prepare-release tests passed'
