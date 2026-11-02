package validate

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"

	"github.com/go-playground/validator/v10"
)

var ErrInvalidConfig = errors.New("invalid configuration")

//nolint:gochecknoglobals
var instance = sync.OnceValue(newValidator)

func Struct(value any) error {
	err := instance().Struct(value)
	if err == nil {
		return nil
	}

	var fieldErrs validator.ValidationErrors
	if !errors.As(err, &fieldErrs) || len(fieldErrs) == 0 {
		return fmt.Errorf("%w: %w", ErrInvalidConfig, err)
	}

	return fmt.Errorf("%w: %s %s", ErrInvalidConfig, fieldErrs[0].Field(), formatRule(fieldErrs[0]))
}

func formatRule(fieldErr validator.FieldError) string {
	if fieldErr.Param() == "" {
		return fieldErr.Tag()
	}

	return fieldErr.Tag() + "=" + fieldErr.Param()
}

func newValidator() *validator.Validate {
	validate := validator.New(validator.WithRequiredStructEnabled())
	validate.RegisterTagNameFunc(tagName)

	return validate
}

func tagName(field reflect.StructField) string {
	name, _, _ := strings.Cut(field.Tag.Get("toml"), ",")
	if name == "" || name == "-" {
		return field.Name
	}

	return name
}
