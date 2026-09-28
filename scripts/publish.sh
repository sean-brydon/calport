#!/usr/bin/env bash
# Publish a Calport release from this Mac: bump the version, build and sign
# the app update, build the CLI and daemons, and create the GitHub release
# the installed apps update from.
#
#   make publish VERSION=0.3.0 [NOTES="What changed"]
#
# The updater key comes from TAURI_SIGNING_PRIVATE_KEY, else from 1Password
# (CALPORT_SIGNING_KEY_OP=op://Vault/Item/field), else ~/.tauri/calport.key,
# else the "Calport updater signing key" item in 1Password's Private vault.
set -euo pipefail

version="${VERSION:?set VERSION, e.g. make publish VERSION=0.3.0}"
version="${version#v}"
tag="v$version"
repo="sean-brydon/calport"
root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$root"

die() { echo "publish: $*" >&2; exit 1; }
[[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || die "VERSION must look like 1.2.3"
[ -z "$(git status --porcelain)" ] || die "commit or stash your changes first"
[ "$(git rev-parse --abbrev-ref HEAD)" = main ] || die "publish from main"
gh release view "$tag" >/dev/null 2>&1 && die "$tag is already released"

if [ -z "${TAURI_SIGNING_PRIVATE_KEY:-}" ]; then
  if [ -n "${CALPORT_SIGNING_KEY_OP:-}" ]; then
    TAURI_SIGNING_PRIVATE_KEY="$(op read "$CALPORT_SIGNING_KEY_OP")"
  elif [ -f "$HOME/.tauri/calport.key" ]; then
    TAURI_SIGNING_PRIVATE_KEY="$(cat "$HOME/.tauri/calport.key")"
  elif command -v op >/dev/null && TAURI_SIGNING_PRIVATE_KEY="$(op read "op://Private/Calport updater signing key/private key" 2>/dev/null)"; then
    :
  else
    die "no updater signing key: set TAURI_SIGNING_PRIVATE_KEY or CALPORT_SIGNING_KEY_OP"
  fi
fi
export TAURI_SIGNING_PRIVATE_KEY TAURI_SIGNING_PRIVATE_KEY_PASSWORD="${TAURI_SIGNING_PRIVATE_KEY_PASSWORD:-}"

echo "Setting version ${version}…"
python3 - "$version" <<'PY'
import json, re, sys
v = sys.argv[1]
for path in ("app/package.json", "app/src-tauri/tauri.conf.json"):
    with open(path) as f:
        data = json.load(f)
    data["version"] = v
    with open(path, "w") as f:
        json.dump(data, f, indent=2)
        f.write("\n")
path = "app/src-tauri/Cargo.toml"
text = open(path).read()
text = re.sub(r'(?m)^version = "[^"]+"', f'version = "{v}"', text, count=1)
open(path, "w").write(text)
PY
(cd app/src-tauri && cargo update -p app --offline >/dev/null 2>&1 || true)

echo "Building the CLI and daemons…"
make release >/dev/null

echo "Building and signing the app…"
make app-build >/dev/null
bundle="app/src-tauri/target/release/bundle"
tarball="$(ls "$bundle"/macos/*.app.tar.gz)"
[ -f "$tarball.sig" ] || die "the updater bundle was not signed"
cp "$tarball" dist/Calport-macos-arm64.app.tar.gz
cp "$tarball.sig" dist/Calport-macos-arm64.app.tar.gz.sig
cp "$bundle"/dmg/*.dmg dist/Calport-macos-arm64.dmg

python3 - "$version" "$tag" "$repo" "${NOTES:-}" <<'PY'
import json, sys, datetime
version, tag, repo, notes = sys.argv[1:5]
sig = open("dist/Calport-macos-arm64.app.tar.gz.sig").read().strip()
feed = {
    "version": version,
    "notes": notes or f"Calport {version}",
    "pub_date": datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
    "platforms": {
        "darwin-aarch64": {
            "signature": sig,
            "url": f"https://github.com/{repo}/releases/download/{tag}/Calport-macos-arm64.app.tar.gz",
        }
    },
}
json.dump(feed, open("dist/latest.json", "w"), indent=2)
PY

git add app/package.json app/src-tauri/tauri.conf.json app/src-tauri/Cargo.toml app/src-tauri/Cargo.lock
git commit -q -m "chore: release $tag"
git tag "$tag"
git push -q origin main "$tag"
gh release create "$tag" dist/* --title "Calport $tag" --notes "${NOTES:-Calport $version}"
echo "Published $tag. Installed apps pick it up within a few hours, or from Settings → Updates → Check now."
