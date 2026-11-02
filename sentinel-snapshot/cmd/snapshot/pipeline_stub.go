//go:build !integration

package main

import (
	"context"
	"errors"
)

var errIntegrationBuildRequired = errors.New("rebuild with -tags integration and the vendored sentinelhub release")

func runPrune(context.Context, pruneArgs) error { return errIntegrationBuildRequired }

func runSnapshot(context.Context, snapshotArgs) error { return errIntegrationBuildRequired }

func runVerify(context.Context, verifyArgs) error { return errIntegrationBuildRequired }
