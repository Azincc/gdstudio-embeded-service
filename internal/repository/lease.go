package repository

import (
	"errors"
	"time"
)

const (
	DefaultLeaseDuration  = 30 * time.Second
	DefaultLeaseHeartbeat = 10 * time.Second
)

var (
	ErrJobCancelled = errors.New("job cancelled")
	ErrLeaseLost    = errors.New("job lease lost")
)
