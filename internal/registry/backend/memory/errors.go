package memory

import (
	"fmt"

	"github.com/Aero-Arc/aero-arc-registry/internal/registry"
)

var (
	errRelayNotRegistered = fmt.Errorf("relay not registered: %w", registry.ErrNotFound)
	errAgentNotRegistered = fmt.Errorf("agent not registered: %w", registry.ErrNotFound)
)
