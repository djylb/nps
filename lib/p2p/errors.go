package p2p

import (
	"context"
	"errors"
)

var (
	ErrNATNotSupportP2P = errors.New("nat type is not support p2p")
)

func mapP2PContextError(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return ErrNATNotSupportP2P
	}
	if err != nil {
		return err
	}
	return ErrNATNotSupportP2P
}
