#!/bin/sh
set -eu

module_dir=$(CDPATH= cd -- "$(dirname "$0")" && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
mkdir -p "$tmp/source/clocks/wit/deps/stale" "$tmp/source/http/wit/deps/stale"
printf 'version = 1\n' > "$tmp/source/wkg.lock"
printf 'nested lock must be removed\n' > "$tmp/source/clocks/wkg.lock"
printf 'stale\n' > "$tmp/source/clocks/wit/deps/stale/package.wit"
printf 'stale\n' > "$tmp/source/http/wit/deps/stale/package.wit"
cat > "$tmp/source/clocks/wit/world.wit" <<'WIT'
package test:clocks@0.1.0;
world clocks {
  import wasi:clocks/wall-clock@0.2.0;
}
WIT
cat > "$tmp/source/http/wit/world.wit" <<'WIT'
package test:http@0.1.0;
world http {
  import wasi:http/types@0.2.2;
}
WIT

(
  cd "$tmp/source"
  dagger -s -m "$module_dir" call wit-fetch --source=. --component-dirs=clocks --component-dirs=http export --path="$tmp/first"
)
grep -q 'name = "wasi:clocks"' "$tmp/first/wkg.lock"
grep -q 'name = "wasi:http"' "$tmp/first/wkg.lock"
test ! -e "$tmp/first/clocks/wkg.lock"
test ! -e "$tmp/first/http/wkg.lock"
test ! -e "$tmp/first/clocks/wit/deps/stale/package.wit"
test ! -e "$tmp/first/http/wit/deps/stale/package.wit"
cp -R "$tmp/first" "$tmp/second-source"
(
  cd "$tmp/second-source"
  dagger -s -m "$module_dir" call wit-fetch --source=. --component-dirs=http --component-dirs=clocks export --path="$tmp/second"
)
diff -ru "$tmp/first" "$tmp/second"
