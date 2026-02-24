package app

import "time"

const (
	// timeoutDetached bounds the wait for detached background tasks.
	timeoutDetached = 5 * time.Second
	// timeoutWorkers bounds the wait for long-running workers after cancel.
	timeoutWorkers = 10 * time.Second
)
