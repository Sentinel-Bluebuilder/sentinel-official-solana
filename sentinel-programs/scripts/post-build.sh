#!/bin/sh
set -eu

cd "$(dirname "$0")/.."
cp target/idl/*.json idl/
go -C clients/go generate ./...
