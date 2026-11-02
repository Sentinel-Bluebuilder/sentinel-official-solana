package core_test

import (
	"errors"
	"testing"

	"github.com/sentinel-official/sentinel-programs/clients/go/core"
)

func TestNewFieldError(t *testing.T) {
	cause := errors.New("invalid value")
	tests := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "field",
			err:  core.NewFieldError("Authority", cause),
			want: "Authority: invalid value",
		},
		{
			name: "index",
			err:  core.NewIndexError(2, cause),
			want: "[2]: invalid value",
		},
		{
			name: "option",
			err:  core.NewOptionError("Maybe", cause),
			want: "?Maybe: invalid value",
		},
		{
			name: "nested",
			err:  core.NewFieldError("Authority", core.NewIndexError(1, core.NewOptionError("Maybe", cause))),
			want: "Authority[1].?Maybe: invalid value",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.Error(); got != tt.want {
				t.Fatalf("Error() = %q, want %q", got, tt.want)
			}
			if !errors.Is(tt.err, cause) {
				t.Fatal("wrapped error does not contain cause")
			}
		})
	}
}

func TestNewFieldError_NilCause(t *testing.T) {
	if core.NewFieldError("Field", nil) != nil {
		t.Fatal("NewFieldError(non-empty field, nil) != nil")
	}
	if core.NewIndexError(1, nil) != nil {
		t.Fatal("NewIndexError(index, nil) != nil")
	}
	if core.NewOptionError("Maybe", nil) != nil {
		t.Fatal("NewOptionError(non-empty field, nil) != nil")
	}
}
