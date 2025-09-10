package commander

import (
	"context"
	"errors"
	"os/exec"

	commonerrors "github.com/psyb0t/common-go/errors"
	"github.com/psyb0t/ctxerrors"
	"github.com/sirupsen/logrus"
)

// executionContext holds common execution data
type executionContext struct {
	ctx context.Context //nolint:containedctx
	// needed for execution context
	cancel     context.CancelFunc
	cmd        *exec.Cmd
	name       string
	args       []string
	timeoutCtx context.Context //nolint:containedctx
	// needed for timeout detection
}

// newExecutionContext creates a new execution context with timeout handling
func (c *commander) newExecutionContext(
	ctx context.Context,
	name string,
	args []string,
	opts *Options,
) *executionContext {
	// Apply timeout if specified
	timeoutCtx, cancel := c.applyTimeout(ctx, opts)

	cmd := c.createCmd(timeoutCtx, name, args, opts)

	return &executionContext{
		ctx:        ctx,
		cancel:     cancel,
		cmd:        cmd,
		name:       name,
		args:       args,
		timeoutCtx: timeoutCtx,
	}
}

// handleExecutionError processes command execution errors
// with timeout detection
func (ec *executionContext) handleExecutionError(err error) error {
	if err == nil {
		logrus.Debugf(
			"command completed successfully: %s %v",
			ec.name,
			ec.args,
		)

		return nil
	}

	logrus.Debugf(
		"command execution failed: %s %v - error: %v",
		ec.name,
		ec.args,
		err,
	)

	// Check for timeout error
	if errors.Is(ec.timeoutCtx.Err(), context.DeadlineExceeded) {
		logrus.Debug("context deadline exceeded, converting to ErrTimeout")

		return commonerrors.ErrTimeout
	}

	return ctxerrors.Wrap(err, "command failed")
}

// cleanup cleans up the execution context
func (ec *executionContext) cleanup() {
	if ec.cancel != nil {
		ec.cancel()
	}
}
