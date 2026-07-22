// Package funding provides deployment-owned platform funding decisions.
package funding

import (
	"context"

	"github.com/deepnoodle-ai/nvoken/internal/ports"
)

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
		return ports.ErrPlatformFundingDenied
	}
	return nil
}
