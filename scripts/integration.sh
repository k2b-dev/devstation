#!/bin/sh
set -eu
DEVSTATION_INTEGRATION=1 go test -race -count=1 -run TestCaddyIntegration -v ./internal/devstation
