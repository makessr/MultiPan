package transfer

import (
	"errors"
	"fmt"
)

// The official form enumerates 0=public, 1=random code, 2=custom code.
// Random and custom shares both require an actual valid code on reads.
func gyShareAccess(row gyShareRecord) (string, error) {
	if row.Type == nil {
		return "", errors.New("mode=missing")
	}
	code := ""
	if row.Code != nil {
		code = *row.Code
	}
	switch *row.Type {
	case 0:
		if code == "-" {
			code = ""
		}
		if code != "" {
			return "", errors.New("mode=0 reason=unexpected-code")
		}
	case 1, 2:
		if row.Code == nil || len(code) != 4 || !shareCode.MatchString(code) {
			return "", fmt.Errorf("mode=%d reason=code-unconfirmed", *row.Type)
		}
	default:
		return "", fmt.Errorf("mode=%d reason=unsupported", *row.Type)
	}
	if row.NeedCode != nil && *row.NeedCode != (code != "") {
		return "", errors.New("reason=password-switch-conflict")
	}
	return code, nil
}
