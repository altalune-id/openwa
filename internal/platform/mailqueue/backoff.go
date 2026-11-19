package mailqueue

import (
	"math/rand/v2"
	"time"
)

func backoffBase(attempt int) time.Duration {
	switch {
	case attempt <= 2:
		return 30 * time.Second
	case attempt == 3:
		return 5 * time.Minute
	case attempt == 4:
		return 30 * time.Minute
	case attempt == 5:
		return 2 * time.Hour
	case attempt == 6:
		return 5 * time.Hour
	default:
		return 10 * time.Hour
	}
}

func backoff(attempt int) time.Duration {
	base := backoffBase(attempt)
	spread := base / 10
	return base - spread + rand.N(2*spread+1)
}
