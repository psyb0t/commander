package commander

import (
	"bytes"
	"context"
	"io"
	"os/exec"

	"github.com/sirupsen/logrus"
)

// Commander executes system commands with context support
type Commander interface {
	// Run executes a command and waits for completion
	Run(
		ctx context.Context,
		name string,
		args []string,
		opts ...Option,
	) error

	// Output executes a command and returns stdout, stderr, and error
	Output(
		ctx context.Context,
		name string,
		args []string,
		opts ...Option,
	) (stdout []byte, stderr []byte, err error)

	// CombinedOutput executes a command and returns combined stdout+stderr and error
	CombinedOutput(
		ctx context.Context,
		name string,
		args []string,
		opts ...Option,
	) (output []byte, err error)

	// Start creates a command that can be controlled manually
	Start(
		ctx context.Context,
		name string,
		args []string,
		opts ...Option,
	) (Process, error)
}

// New creates a new commander instance
func New() Commander { //nolint:ireturn // factory function returns interface by design
	return &commander{}
}

// commander executes actual system commands
type commander struct{}

// applyTimeout creates a timeout context if timeout option is set
func (c *commander) applyTimeout(
	ctx context.Context,
	opts *Options,
) (context.Context, context.CancelFunc) {
	if opts != nil && opts.Timeout != nil && *opts.Timeout > 0 {
		logrus.Debugf("applying timeout: %v", *opts.Timeout)

		return context.WithTimeout(ctx, *opts.Timeout)
	}

	if opts != nil && opts.Timeout != nil && *opts.Timeout <= 0 {
		logrus.Debugf("timeout duration %v is <= 0, treating as no timeout", *opts.Timeout)

		return ctx, func() {}
	}

	logrus.Debug("no timeout specified, using original context")
	// Return a no-op cancel function if no timeout

	return ctx, func() {}
}

func (c *commander) Run(
	ctx context.Context,
	name string,
	args []string,
	opts ...Option,
) error {
	options := c.buildOptions(opts...)

	exec := c.newExecutionContext(ctx, name, args, options)
	defer exec.cleanup()

	logrus.Debugf("running command: %s %v", name, args)

	err := exec.cmd.Run()

	return exec.handleExecutionError(err)
}

func (c *commander) Output(
	ctx context.Context,
	name string,
	args []string,
	opts ...Option,
) ([]byte, []byte, error) {
	var stdoutBuf, stderrBuf bytes.Buffer

	err := c.runWithOutput(ctx, name, args, &stdoutBuf, &stderrBuf, opts...)

	return stdoutBuf.Bytes(), stderrBuf.Bytes(), err
}

func (c *commander) CombinedOutput(
	ctx context.Context,
	name string,
	args []string,
	opts ...Option,
) ([]byte, error) {
	var combinedBuf bytes.Buffer

	err := c.runWithOutput(ctx, name, args, &combinedBuf, &combinedBuf, opts...)

	return combinedBuf.Bytes(), err
}

// runWithOutput is the private method that does the actual work
func (c *commander) runWithOutput(
	ctx context.Context,
	name string,
	args []string,
	stdoutBuf io.Writer,
	stderrBuf io.Writer,
	opts ...Option,
) error {
	options := c.buildOptions(opts...)

	exec := c.newExecutionContext(ctx, name, args, options)
	defer exec.cleanup()

	// Set up the provided buffers
	exec.cmd.Stdout = stdoutBuf
	exec.cmd.Stderr = stderrBuf

	logrus.Debugf("running command for output: %s %v", name, args)

	runErr := exec.cmd.Run()

	logrus.Debug("command output captured")

	// Handle error but return outputs regardless (matches exec.Cmd behavior)
	return exec.handleExecutionError(runErr)
}

//nolint:ireturn // interface return by design
func (c *commander) Start(
	ctx context.Context,
	name string,
	args []string,
	opts ...Option,
) (Process, error) {
	options := c.buildOptions(opts...)

	// Apply timeout if specified - this affects the entire process lifecycle
	timeoutCtx, cancel := c.applyTimeout(ctx, options)
	// Note: We don't defer cancel() here because the process might run longer
	// The cancel will be handled when the process finishes

	cmd := c.createCmd(timeoutCtx, name, args, options)

	logrus.Debugf("starting command: %s %v", name, args)

	proc := c.newProcess(cmd, cancel, timeoutCtx)

	if err := proc.Start(); err != nil {
		cancel() // Clean up on error

		return nil, err
	}

	return proc, nil
}

// newProcess creates a new process instance
func (c *commander) newProcess(
	cmd *exec.Cmd,
	cancel context.CancelFunc,
	timeoutCtx context.Context, //nolint:revive // context not first param by design
) *process {
	return &process{
		cmd:            cmd,
		internalStdout: make(chan string), // Unbuffered - always drained by discardInternalOutput
		internalStderr: make(chan string),
		doneCh:         make(chan struct{}), // Signal channel to stop all goroutines
		waitCh:         make(chan struct{}), // Closed when cmd.Wait() completes
		cancelTimeout:  cancel,              // Store cancel function for cleanup
		timeoutCtx:     timeoutCtx,          // Store timeout context for error checking
	}
}

func (c *commander) buildOptions(opts ...Option) *Options {
	options := &Options{}
	for _, opt := range opts {
		opt(options)
	}

	return options
}

func (c *commander) createCmd(
	ctx context.Context,
	name string,
	args []string,
	opts *Options,
) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)

	if opts != nil {
		cmd.Stdin = opts.Stdin
		cmd.Env = opts.Env
		cmd.Dir = opts.Dir
	}

	return cmd
}
