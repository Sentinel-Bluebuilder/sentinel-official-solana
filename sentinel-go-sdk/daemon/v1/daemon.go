package daemonv1

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/sentinel-official/sentinel-go-sdk/v3/version"
)

const SocketFileName = "daemon.sock"

var (
	ErrFailedPrecondition = errors.New("failed precondition")
	ErrInvalidArgument    = errors.New("invalid argument")
	ErrNotFound           = errors.New("not found")
	ErrUnavailable        = errors.New("unavailable")
)

//nolint:wrapcheck
func ToStatus(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrInvalidArgument):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, ErrFailedPrecondition):
		return status.Error(codes.FailedPrecondition, err.Error())
	case errors.Is(err, ErrNotFound):
		return status.Error(codes.NotFound, err.Error())
	case errors.Is(err, ErrUnavailable):
		return status.Error(codes.Unavailable, err.Error())
	case errors.Is(err, context.Canceled):
		return status.Error(codes.Canceled, err.Error())
	case errors.Is(err, context.DeadlineExceeded):
		return status.Error(codes.DeadlineExceeded, err.Error())
	default:
		return status.Error(codes.Internal, err.Error())
	}
}

func VersionFrom(info version.Info) *Version {
	return &Version{
		Commit:    info.Commit,
		Tag:       info.Tag,
		Network:   info.Network,
		ProgramId: info.ProgramID,
		Mint:      info.Mint,
	}
}
