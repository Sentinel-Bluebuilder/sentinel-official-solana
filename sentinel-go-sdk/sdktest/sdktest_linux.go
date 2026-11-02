package sdktest

import (
	"github.com/sentinel-official/sentinel-go-sdk/v3/service"
)

var _ service.Egress = (*FakeEgress)(nil)
