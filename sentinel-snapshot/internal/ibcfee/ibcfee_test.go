//go:build integration

package ibcfee_test

import (
	"testing"

	"github.com/sentinel-official/sentinel-snapshot/internal/ibcfee"
)

func TestEnabled(t *testing.T) {
	t.Parallel()

	if got := ibcfee.Enabled(); !got {
		t.Fatalf("Enabled = %v, want true (measured: ibcfee.AppModuleBasic{} is registered "+
			"in the vendored v12.0.2 app's ModuleBasics)", got)
	}
}
