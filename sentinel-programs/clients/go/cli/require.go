package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/pflag"
)

var ErrMissingFlag = errors.New("missing flag")

func Require(fs *pflag.FlagSet, names ...string) error {
	for _, name := range names {
		if !fs.Changed(name) {
			return fmt.Errorf("%w: --%s", ErrMissingFlag, name)
		}
	}

	return nil
}
