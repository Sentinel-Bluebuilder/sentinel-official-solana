//go:build integration

package ibcescrow

func ValidateNoDuplicateAddresses(channels []Channel) ([]Channel, error) {
	return validateNoDuplicateAddresses(channels)
}
