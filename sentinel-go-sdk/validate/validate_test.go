package validate_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-go-sdk/v3/validate"
)

type nested struct {
	Inner sample `toml:"inner"`
}

type sample struct {
	Name string `toml:"name"        validate:"required"`
	Port uint16 `toml:"listen_port" validate:"gte=1024"`
}

func TestStruct(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		value   any
		message string
		invalid bool
	}{
		{name: "valid", value: sample{Name: "x", Port: 2000}},
		{
			name:    "low_port",
			value:   sample{Name: "x", Port: 80},
			message: "invalid configuration: listen_port gte=1024",
			invalid: true,
		},
		{name: "missing_name", value: sample{Port: 2000}, message: "invalid configuration: name required"},
		{name: "nested", value: nested{Inner: sample{Port: 2000}}, message: "invalid configuration: name required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := validate.Struct(tc.value)
			if tc.message == "" {
				require.NoError(t, err)

				return
			}

			if tc.invalid {
				require.ErrorIs(t, err, validate.ErrInvalidConfig)
			}

			require.EqualError(t, err, tc.message)
		})
	}
}
