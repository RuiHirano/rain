#!/bin/bash

set -eoux pipefail

go build ./cmd/rain
staticcheck ./...
staticcheck -tags=func_test ./...
go vet ./...
go vet -tags=func_test ./...
go test ./...
go test -tags=func_test ./...
