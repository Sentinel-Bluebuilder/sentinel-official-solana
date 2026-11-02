package strandcheck_test

func repeatedAddressBytes(value byte) [20]byte {
	var address [20]byte
	for i := range address {
		address[i] = value
	}

	return address
}
