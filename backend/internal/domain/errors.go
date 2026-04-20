package domain

import "errors"

// Sentinel errors used throughout the domain. Packages should wrap
// these with errors.Is for checking.
var (
	ErrNotFound         = errors.New("not found")
	ErrConflict         = errors.New("conflict")
	ErrValidation       = errors.New("validation error")
	ErrInsufficientFund = errors.New("insufficient funds")
	ErrRiskRejected     = errors.New("rejected by risk engine")
	ErrBrokerRejected   = errors.New("rejected by broker")
	ErrProviderFailure  = errors.New("provider failure")
	ErrUnauthorized     = errors.New("unauthorized")
)
