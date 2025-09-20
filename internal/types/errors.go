package types

import (
	"errors"
	"fmt"
)

var (
	ErrNotFound            = errors.New("not found")
	ErrPrecondition        = errors.New("precondition failed")
	ErrInvalidClientConfig = errors.New("invalid client config")

	ErrInvalidBackend  = errors.New("invalid backend")
	ErrDataStoreAccess = errors.New("data store read/write error")
)

func Err(typedError error, innerErr error, msgTemplate string, args ...any) error {
	if msgTemplate == "" {
		return errors.Join(typedError, innerErr)
	} else {
		return errors.Join(typedError, innerErr, fmt.Errorf(msgTemplate, args...))
	}
}
