// Package linked implements the project-owned declaration side of the private
// whole-project generation boundary. It does not generate or publish files.
package linked

import (
	"context"
	"errors"
	"io"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/internal/projectgenerate/protocol"
)

// Loader returns the complete declaration-owned project specification.
type Loader func(context.Context) (codegen.ProjectSpec, error)

// Report contains only calls and completed writes observed by one invocation.
type Report struct {
	CommandDispatches    int
	LoaderCalls          int
	RunnerResponseWrites int
}

// Run executes the sole private linked command. Completed request or loader
// failures are written as closed responses. Cancellation and I/O failures are
// returned as Go errors.
func Run(
	ctx context.Context,
	loader Loader,
	argv []string,
	stdin io.Reader,
	stdout io.Writer,
) (Report, error) {
	var report Report
	arguments := append([]string(nil), argv...)
	if ctx == nil {
		return report, errors.New("project generation linked: nil context")
	}
	if stdin == nil {
		return report, errors.New("project generation linked: nil stdin")
	}
	if stdout == nil {
		return report, errors.New("project generation linked: nil stdout")
	}
	if len(arguments) != 1 || arguments[0] != protocol.PrivateArgument {
		return report, errors.New("project generation linked: invalid private argv")
	}
	if err := ctx.Err(); err != nil {
		return report, err
	}

	failure, failed, err := protocol.ReadRequest(stdin)
	if err != nil {
		return report, err
	}
	if failed {
		return completeResponse(ctx, stdout, report, protocol.Response{Failure: failure})
	}
	report.CommandDispatches = 1
	if err := ctx.Err(); err != nil {
		return report, err
	}
	if loader == nil {
		return completeResponse(ctx, stdout, report, protocol.Response{Failure: protocol.Failure{
			Category: protocol.CategoryDeclaration,
			Code:     protocol.CodeProjectSpecLoadFailed,
		}})
	}

	report.LoaderCalls = 1
	spec, loadErr := loader(ctx)
	if err := ctx.Err(); err != nil {
		return report, err
	}
	if loadErr != nil {
		return completeResponse(ctx, stdout, report, protocol.Response{Failure: protocol.Failure{
			Category: protocol.CategoryDeclaration,
			Code:     protocol.CodeProjectSpecLoadFailed,
		}})
	}
	if err := protocol.ValidateProjectSpec(spec); err != nil {
		return completeResponse(ctx, stdout, report, protocol.Response{Failure: protocol.Failure{
			Category: protocol.CategoryDeclaration,
			Code:     protocol.CodeProjectSpecLoadFailed,
		}})
	}
	if err := codegen.ValidateProjectSpec(spec); err != nil {
		return completeResponse(ctx, stdout, report, protocol.Response{Failure: protocol.Failure{
			Category: protocol.CategoryDeclaration,
			Code:     protocol.CodeProjectSpecLoadFailed,
		}})
	}
	return completeResponse(ctx, stdout, report, protocol.Response{OK: true, ProjectSpec: spec})
}

func completeResponse(
	ctx context.Context,
	writer io.Writer,
	report Report,
	response protocol.Response,
) (Report, error) {
	if err := ctx.Err(); err != nil {
		return report, err
	}
	report.RunnerResponseWrites++
	if err := protocol.WriteResponse(writer, response); err != nil {
		return report, err
	}
	return report, nil
}
