// Package clock provides the real system clock implementing port.Clock.
package clock

import (
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

// Real is the system clock.
type Real struct{}

var _ port.Clock = Real{}

// Now returns the current time.
func (Real) Now() time.Time { return time.Now() }
