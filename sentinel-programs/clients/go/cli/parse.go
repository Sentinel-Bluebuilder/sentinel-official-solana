package cli

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strconv"
	"strings"

	"github.com/solana-foundation/solana-go/v2"
)

func ParseAccountMeta(s string) (*solana.AccountMeta, error) {
	parts := strings.Split(s, ":")

	publicKey, err := solana.PublicKeyFromBase58(parts[0])
	if err != nil {
		return nil, err
	}

	m := solana.NewAccountMeta(publicKey, false, false)

	for _, f := range parts[1:] {
		switch f {
		case "w":
			m.IsWritable = true
		case "s":
			m.IsSigner = true
		default:
			return nil, fmt.Errorf("unknown account flag %q", f)
		}
	}

	return m, nil
}

func ParseBool(s string) (bool, error) {
	return strconv.ParseBool(s)
}

func ParseByteArray[A any](s string) (A, error) {
	var a A

	v := reflect.ValueOf(&a).Elem()
	if v.Kind() != reflect.Array || v.Type().Elem().Kind() != reflect.Uint8 {
		return a, fmt.Errorf("%T is not a byte array", a)
	}

	return a, parseFixedBytes(v.Slice(0, v.Len()).Bytes(), s)
}

func ParseBytes(s string) ([]byte, error) {
	if strings.HasPrefix(s, "0x") {
		return hex.DecodeString(s[2:])
	}

	return []byte(s), nil
}

func ParseFloat[T ~float32 | ~float64](s string) (T, error) {
	v, err := strconv.ParseFloat(s, reflect.TypeFor[T]().Bits())

	return T(v), err
}

func ParseInt[T ~int8 | ~int16 | ~int32 | ~int64](s string) (T, error) {
	v, err := strconv.ParseInt(s, 0, reflect.TypeFor[T]().Bits())

	return T(v), err
}

func ParseJSON[T any](s string) (T, error) {
	var v T

	data := []byte(s)

	if strings.HasPrefix(s, "@") {
		var err error

		data, err = os.ReadFile(s[1:])
		if err != nil {
			return v, err
		}
	}

	if err := checkJSONShape(data, reflect.TypeFor[T]()); err != nil {
		return v, err
	}

	return v, json.Unmarshal(data, &v)
}

func ParsePublicKey(s string) (solana.PublicKey, error) {
	return solana.PublicKeyFromBase58(s)
}

func ParseString(s string) (string, error) {
	return s, nil
}

func ParseUint[T ~uint8 | ~uint16 | ~uint32 | ~uint64](s string) (T, error) {
	v, err := strconv.ParseUint(s, 0, reflect.TypeFor[T]().Bits())

	return T(v), err
}
