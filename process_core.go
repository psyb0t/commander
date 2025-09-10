package commander

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"strings"
	"sync"
	"syscall"

	commonerrors "github.com/psyb0t/common-go/errors"
	"github.com/psyb0t/ctxerrors"
	"github.com/sirupsen/logrus"
)

// Process represents a running command
type Process interface {
	// Start starts the process and sets up all the pipes and goroutines
	Start() error

	// Wait waits for the process to complete
	Wait() error

	// StdinPipe returns a pipe connected to stdin
	StdinPipe() (io.WriteCloser, error)

	// Stream sends live output to separate stdout and stderr channels
	// Starts streaming from the current moment, not from the beginning
	// Multiple streams can be active simultaneously (broadcast)
	// Pass nil for channels you don't want to listen to
	Stream(stdout, stderr chan<- string)

	// Stop terminates the process gracefully with context deadline, then kills forcefully
	// First tries SIGTERM (or custom signal), then SIGKILL after context deadline
	Stop(ctx context.Context, opts ...StopOption) error

	// Kill immediately terminates the process with SIGKILL (no graceful period)
	// Equivalent to Stop(ctx) with zero timeout context
	Kill(ctx context.Context) error
}

// streamChannels holds separate stdout/stderr channels
type streamChannels struct {
	stdout chan<- string
	stderr chan<- string
}

// process wraps exec.Cmd for the Process interface
type process struct {
	cmd *exec.Cmd

	internalStdout chan string      // Always created and read from
	internalStderr chan string      // Always created and read from
	streamChans    []streamChannels // Active stream channels
	streamMu       sync.Mutex       // Protects streamChans
	doneCh         chan struct{}    // Signal to stop all goroutines
	// and close channels
	stopOnce sync.Once // Ensure Stop() is only called
	// once (master cleanup)
	cmdWaitOnce   sync.Once          // Ensure cmd.Wait() is only called once
	cmdWaitResult error              // Result from cmd.Wait()
	waitCh        chan struct{}      // Closed when cmd.Wait() completes
	cancelTimeout context.CancelFunc // Cancel function for timeout context
	timeoutCtx    context.Context    //nolint:containedctx
	// needed for timeout error detection
}

// cmdWait ensures cmd.Wait() is only called once, even from multiple goroutines
// All concurrent calls wait for the actual process to finish
func (p *process) cmdWait() error {
	p.cmdWaitOnce.Do(func() {
		logrus.Debug("calling cmd.Wait() (protected by sync.Once)")

		p.cmdWaitResult = p.cmd.Wait()
		logrus.Debugf("cmd.Wait() completed with result: %v", p.cmdWaitResult)
		// Signal that cmd.Wait() has completed
		close(p.waitCh)
	})

	// Wait for cmd.Wait() to complete (either from this goroutine or another)
	<-p.waitCh

	return p.cmdWaitResult
}

// Wait waits for the process to complete
func (p *process) Wait() error {
	logrus.Debug("waiting for process to complete")

	defer func() {
		// Always ensure stop happens when Wait() completes
		_ = p.Stop(context.Background())
	}()

	// Process should already be started by Start() - just wait for it
	if p.cmd.Process != nil {
		logrus.Debugf("waiting for process PID %d to finish", p.cmd.Process.Pid)
	} else {
		logrus.Debug("waiting for process to finish (no PID available)")
	}

	err := p.cmdWait()

	if p.cmd.Process != nil {
		logrus.Debugf("process PID %d finished", p.cmd.Process.Pid)
	} else {
		logrus.Debug("process finished")
	}

	if err != nil {
		logrus.Debugf("process wait failed with error: %v", err)
		// Check if this was a timeout error
		// (specifically DeadlineExceeded, not just any context error)
		if p.timeoutCtx != nil &&
			errors.Is(p.timeoutCtx.Err(), context.DeadlineExceeded) {
			logrus.Debug("process failed due to timeout")

			return commonerrors.ErrTimeout
		}

		// Check if process was terminated by SIGTERM
		if isTerminatedBySignal(err) {
			logrus.Debug("process was terminated by SIGTERM")

			return commonerrors.ErrTerminated
		}

		// Check if process was killed by SIGKILL
		if isKilledBySignal(err) {
			logrus.Debug("process was killed by SIGKILL")

			return commonerrors.ErrKilled
		}

		return ctxerrors.Wrap(err, "process wait failed")
	}

	logrus.Debug("process completed successfully")

	return nil
}

// StdinPipe returns a pipe connected to stdin
func (p *process) StdinPipe() (io.WriteCloser, error) {
	pipe, err := p.cmd.StdinPipe()
	if err != nil {
		return nil, ctxerrors.Wrap(err, "failed to get stdin pipe")
	}

	return pipe, nil
}

// Kill immediately terminates the process with SIGKILL (no graceful period)
func (p *process) Kill(ctx context.Context) error {
	logrus.Debug("kill requested - performing immediate force kill")

	// Create context with no deadline to force immediate kill
	return p.Stop(ctx)
}

// isHarmlessWaitError checks if the error is a harmless
// "no child processes" error
// that can occur during process cleanup and should be ignored
func isHarmlessWaitError(err error) bool {
	return err != nil &&
		strings.Contains(err.Error(), "waitid: no child processes")
}

// getTerminationSignal checks if the process was terminated by a
// signal and returns the signal
func getTerminationSignal(err error) syscall.Signal {
	if err == nil {
		return 0
	}

	var exitError *exec.ExitError
	if errors.As(err, &exitError) {
		if status, ok := exitError.Sys().(syscall.WaitStatus); ok {
			// Check if process was terminated by a signal (not exited normally)
			if status.Signaled() {
				signal := status.Signal()
				logrus.Debugf("process terminated by signal: %v", signal)

				return signal
			}
		}
	}

	return 0
}

// isTerminatedBySignal checks if the process was terminated by SIGTERM
func isTerminatedBySignal(err error) bool {
	return getTerminationSignal(err) == syscall.SIGTERM
}

// isKilledBySignal checks if the process was terminated by SIGKILL
func isKilledBySignal(err error) bool {
	return getTerminationSignal(err) == syscall.SIGKILL
}
