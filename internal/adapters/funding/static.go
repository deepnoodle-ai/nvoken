// Package funding provides deployment-owned platform funding decisions.
package funding

import (
	"context"
	"errors"
)

var ErrFundingDenied = errors.New("platform funding denied")

type StaticGate struct {
	Allowed bool
}

func (g StaticGate) AuthorizePlatformModelCall(
	context.Context,
	string,
	string,
	string,
	string,
) error {
	if !g.Allowed {
		return ErrFundingDenied
	}
	return nil
}
