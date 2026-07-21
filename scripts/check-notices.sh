#!/bin/sh
set -eu

actual="$(
  go list -deps -f '{{with .Module}}{{if not .Main}}{{.Path}}{{end}}{{end}}' ./cmd/nanit-controller |
    sed '/^$/d' |
    sort -u
)"
expected='github.com/gorilla/websocket
google.golang.org/protobuf'

if [ "$actual" != "$expected" ]; then
  printf '%s\n' 'linked module inventory changed; update THIRD_PARTY_NOTICES.md' >&2
  printf '%s\n' 'expected:' "$expected" 'actual:' "$actual" >&2
  exit 1
fi

for module in $expected; do
  grep -Fq "$module" THIRD_PARTY_NOTICES.md || {
    printf 'missing notice for %s\n' "$module" >&2
    exit 1
  }
done
