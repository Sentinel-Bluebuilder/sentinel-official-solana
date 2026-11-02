package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/sentinel-official/sentinel-go-sdk/v3/daemon/instance"
)

const (
	daemonCallTimeout = 30 * time.Second

	roleClient   = "client"
	roleNode     = "node"
	roleProvider = "provider"
)

var errRoleNotServed = errors.New("role is not served")

type callSpec[Resp any] struct {
	AddFlags func(*pflag.FlagSet)
	Args     cobra.PositionalArgs
	Render   func(*Resp) any
	Role     string
	Short    string
	Use      string
}

type streamCall[C, Req, Resp any] func(
	C,
	context.Context,
	*Req,
	...grpc.CallOption,
) (grpc.ServerStreamingClient[Resp], error)

type unaryCall[C, Req, Resp any] func(C, context.Context, *Req, ...grpc.CallOption) (*Resp, error)

func addInstanceFlag(cmd *cobra.Command, tagFlag *string) {
	cmd.Flags().StringVar(tagFlag, "instance", "", "daemon instance `tag`: c<N>, n<N> or p<N>")
	_ = cmd.MarkFlagRequired("instance")
}

func newStreamCallCmd[C, Req, Resp any](
	daemon *dialer,
	printer *Printer,
	spec callSpec[Resp],
	newClient func(grpc.ClientConnInterface) C,
	open streamCall[C, Req, Resp],
	newRequest func(*cobra.Command, []string) (*Req, error),
) *cobra.Command {
	var tagFlag string

	run := func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		req := new(Req)

		if newRequest != nil {
			var err error

			req, err = newRequest(cmd, args)
			if err != nil {
				return err
			}
		}

		tag, err := instance.Parse(tagFlag)
		if err != nil {
			return err
		}

		conn, err := daemon.Dial(ctx, tag)
		if err != nil {
			return err
		}
		defer func() { _ = conn.Close() }()

		stream, err := open(newClient(conn), ctx, req)
		if err != nil {
			return wrapCallError(spec.Role, daemon.endpointFor(tag), err)
		}

		for {
			resp, err := stream.Recv()
			if errors.Is(err, io.EOF) {
				return nil
			}

			if err != nil {
				return wrapCallError(spec.Role, daemon.endpointFor(tag), err)
			}

			var result any = resp
			if spec.Render != nil {
				result = spec.Render(resp)
			}

			if err := printer.Print(result); err != nil {
				return err
			}
		}
	}

	cmd := &cobra.Command{
		Args:  spec.Args,
		RunE:  run,
		Short: spec.Short,
		Use:   spec.Use,
	}
	addInstanceFlag(cmd, &tagFlag)

	if spec.AddFlags != nil {
		spec.AddFlags(cmd.Flags())
	}

	return cmd
}

func newUnaryCallCmd[C, Req, Resp any](
	daemon *dialer,
	printer *Printer,
	spec callSpec[Resp],
	newClient func(grpc.ClientConnInterface) C,
	call unaryCall[C, Req, Resp],
	newRequest func(*cobra.Command, []string) (*Req, error),
) *cobra.Command {
	var tagFlag string

	run := func(cmd *cobra.Command, args []string) error {
		ctx, cancel := context.WithTimeout(cmd.Context(), daemonCallTimeout)
		defer cancel()

		req := new(Req)

		if newRequest != nil {
			var err error

			req, err = newRequest(cmd, args)
			if err != nil {
				return err
			}
		}

		tag, err := instance.Parse(tagFlag)
		if err != nil {
			return err
		}

		conn, err := daemon.Dial(ctx, tag)
		if err != nil {
			return err
		}
		defer func() { _ = conn.Close() }()

		resp, err := call(newClient(conn), ctx, req)
		if err != nil {
			return wrapCallError(spec.Role, daemon.endpointFor(tag), err)
		}

		if spec.Render != nil {
			return printer.Print(spec.Render(resp))
		}

		return printer.Print(resp)
	}

	cmd := &cobra.Command{
		Args:  spec.Args,
		RunE:  run,
		Short: spec.Short,
		Use:   spec.Use,
	}
	addInstanceFlag(cmd, &tagFlag)

	if spec.AddFlags != nil {
		spec.AddFlags(cmd.Flags())
	}

	return cmd
}

func wrapCallError(role, endpoint string, err error) error {
	if role == "" {
		role = "daemon"
	}

	if status.Code(err) == codes.Unimplemented {
		return fmt.Errorf("%s %w: %w", role, errRoleNotServed, err)
	}

	if status.Code(err) == codes.Unavailable {
		return fmt.Errorf("calling daemon at %s: %w", endpoint, err)
	}

	return fmt.Errorf("calling daemon: %w", err)
}
