//go:build integration

package sdktest

type PortSetForTest = portSet

func NewPortSetForTest() *PortSetForTest {
	return &portSet{taken: map[int]bool{}}
}

func ReservePortsForTest(set *PortSetForTest, ports ...int) bool {
	return set.reserve(ports...)
}
