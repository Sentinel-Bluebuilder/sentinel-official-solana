package v2ray

import (
	"github.com/sentinel-official/sentinel-go-sdk/v3/service"
)

type API = api

func CheckVersion(out []byte, err error, severity service.Severity) []service.PreflightCheck {
	return checkVersion(out, err, severity)
}

func ErrorFromChecks(checks []service.PreflightCheck) error { return errorFromChecks(checks) }
