package commander

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"syscall"
	"time"

	commonerrors "github.com/psyb0t/common-go/errors"
	"github.com/psyb0t/ctxerrors"
	"github.com/sirupsen/logrus"
)

// Start starts the process and sets up all the pipes and goroutines
func (p *process) Start() error {
	logrus.Debug("creating process pipes for command")

	// Set up pipes BEFORE starting
	stdout, err := p.cmd.StdoutPipe()
	if err != nil {
		logrus.Debugf("failed to create stdout pipe - error: %v", err)

		return ctxerrors.Wrap(err, "failed to get stdout pipe")
	}

	stderr, err := p.cmd.StderrPipe()
	if err != nil {
		logrus.Debugf("failed to create stderr pipe - error: %v", err)

		return ctxerrors.Wrap(err, "failed to get stderr pipe")
	}

	logrus.Debug("starting process")

	if err := p.cmd.Start(); err != nil {
		logrus.Debugf("failed to start process - error: %v", err)

		return ctxerrors.Wrap(err, "failed to start command")
	}

	if p.cmd.Process != nil {
		logrus.Debugf("process started successfully - PID: %d", p.cmd.Process.Pid)
	} else {
		logrus.Debug("process started but no PID available")
	}

	logrus.Debug("starting background goroutines for process")
	// Start background goroutines to read from pipes into internal channels
	go p.readStdout(stdout)
	go p.readStderr(stderr)

	// Start the discard goroutine to keep internal channels flowing
	go p.discardInternalOutput()

	logrus.Debug("process initialization complete")

	return nil
}

// readStdout reads from stdout pipe and sends to internal channel
func (p *process) readStdout(stdout io.ReadCloser) {
	logrus.Debug("starting stdout reader goroutine")

	defer func() {
		logrus.Debug("closing stdout pipe and internal channel")

		_ = stdout.Close() // Ignore close error - nothing we can do

		close(p.internalStdout) // Close internal channel when done
	}()

	scanner := bufio.NewScanner(stdout)
	lineCount := 0

	for scanner.Scan() {
		line := scanner.Text()
		lineCount++

		select {
		case <-p.doneCh:
			// Process is done, stop reading
			logrus.Debugf(
				"stdout reader stopping after %d lines (process done)",
				lineCount,
			)

			return
		case p.internalStdout <- line:
			logrus.Debugf("stdout line %d: %s", lineCount, line)
		}
	}

	if err := scanner.Err(); err != nil {
		logrus.Debugf(
			"stdout scanner error after %d lines: %v",
			lineCount,
			err,
		)

		return
	}

	logrus.Debugf(
		"stdout reader finished successfully after %d lines",
		lineCount,
	)
}

// readStderr reads from stderr pipe and sends to internal channel
func (p *process) readStderr(stderr io.ReadCloser) {
	logrus.Debug("starting stderr reader goroutine")

	defer func() {
		logrus.Debug("closing stderr pipe and internal channel")

		_ = stderr.Close() // Ignore close error - nothing we can do

		close(p.internalStderr) // Close internal channel when done
	}()

	scanner := bufio.NewScanner(stderr)
	lineCount := 0

	for scanner.Scan() {
		line := scanner.Text()
		lineCount++

		select {
		case <-p.doneCh:
			// Process is done, stop reading
			logrus.Debugf(
				"stderr reader stopping after %d lines (process done)",
				lineCount,
			)

			return
		case p.internalStderr <- line:
			logrus.Debugf("stderr line %d: %s", lineCount, line)
		}
	}

	if err := scanner.Err(); err != nil {
		logrus.Debugf(
			"stderr scanner error after %d lines: %v",
			lineCount,
			err,
		)

		return
	}

	logrus.Debugf(
		"stderr reader finished successfully after %d lines",
		lineCount,
	)
}

// Stop terminates the process gracefully with context deadline, then kills forcefully
func (p *process) Stop(
	ctx context.Context,
	opts ...StopOption,
) error {
	var stopErr error

	// Use sync.Once to ensure all cleanup happens exactly once
	p.stopOnce.Do(func() {
		logrus.Debug("performing master stop and cleanup")

		// Step 1: Stop/kill the actual process if it's running
		if p.cmd.Process != nil {
			logrus.Debugf("stopping process PID %d", p.cmd.Process.Pid)
			// Build stop options
			options := &StopOptions{
				Signal: syscall.SIGTERM, // Default signal
			}
			for _, opt := range opts {
				opt(options)
			}

			stopErr = p.killProcess(ctx, options)
		} else {
			logrus.Debug("stop requested but process has no PID - cleaning up anyway")
		}

		// Step 2: Cleanup all resources (always happens regardless of kill result)
		logrus.Debug("performing resource cleanup")

		// Clean up timeout cancel function
		if p.cancelTimeout != nil {
			logrus.Debug("cleaning up timeout cancel function")
			p.cancelTimeout()
		}

		// Signal all goroutines to stop and close channels
		logrus.Debug("signaling all goroutines to stop")
		close(p.doneCh)

		// Close stream channels
		p.closeStreamChannels()

		logrus.Debug("master stop and cleanup complete")
	})

	return stopErr
}

// killProcess handles the actual process termination with context deadline
func (p *process) killProcess(
	ctx context.Context,
	options *StopOptions,
) error {
	// Check if context has no deadline, skip graceful termination and
	// force kill immediately
	_, hasDeadline := ctx.Deadline()
	if !hasDeadline {
		logrus.Debugf(
			"no deadline in context for process PID %d, "+
				"force killing immediately",
			p.cmd.Process.Pid,
		)

		return p.forceKill()
	}

	// First try graceful termination with specified signal
	logrus.Debugf(
		"attempting graceful termination (%v) for process PID %d",
		options.Signal,
		p.cmd.Process.Pid,
	)

	if err := p.cmd.Process.Signal(options.Signal); err != nil {
		logrus.Debugf(
			"failed to send %v to process PID %d, forcing kill: %v",
			options.Signal,
			p.cmd.Process.Pid,
			err,
		)
		// If we can't signal, try killing immediately
		return p.forceKill()
	}

	return p.waitForGracefulShutdown(ctx, options.Signal)
}

// waitForGracefulShutdown waits for process to exit or context deadline
func (p *process) waitForGracefulShutdown(
	ctx context.Context,
	signal syscall.Signal,
) error {
	deadline, _ := ctx.Deadline()
	timeout := time.Until(deadline)

	// Wait for graceful shutdown or force kill after context deadline
	logrus.Debugf(
		"%v sent to process PID %d, waiting %v for graceful shutdown",
		signal,
		p.cmd.Process.Pid,
		timeout,
	)

	// Wait for either graceful shutdown or context deadline
	done := make(chan error, 1)

	go func() {
		// Wait for process to end
		done <- p.cmdWait()
	}()

	select {
	case err := <-done:
		// Process exited gracefully
		if isHarmlessWaitError(err) {
			return nil
		}

		// Check if process was terminated by our signal
		if getTerminationSignal(err) == signal {
			logrus.Debugf("process gracefully terminated by %v", signal)

			return commonerrors.ErrTerminated
		}

		// Check if process was killed by SIGKILL
		if isKilledBySignal(err) {
			logrus.Debug("process was killed by SIGKILL")

			return commonerrors.ErrKilled
		}

		return err

	case <-ctx.Done():
		// Context deadline reached, force kill
		logrus.Debugf(
			"graceful shutdown deadline reached for process PID %d, "+
				"force killing",
			p.cmd.Process.Pid,
		)

		return p.forceKill()
	}
}

// forceKill immediately kills the process with SIGKILL
func (p *process) forceKill() error {
	logrus.Debugf("force killing process PID %d (SIGKILL)", p.cmd.Process.Pid)

	if err := p.cmd.Process.Kill(); err != nil {
		// If process is already finished, that's fine
		if errors.Is(err, os.ErrProcessDone) {
			logrus.Debugf("process PID %d was already finished", p.cmd.Process.Pid)

			return nil
		}

		logrus.Debugf(
			"failed to force kill process PID %d: %v",
			p.cmd.Process.Pid,
			err,
		)

		return ctxerrors.Wrap(err, "failed to force kill process")
	}

	logrus.Debugf(
		"SIGKILL sent to process PID %d, waiting for process to exit",
		p.cmd.Process.Pid,
	)

	// Wait for process to exit after SIGKILL
	err := p.cmdWait()
	if err != nil {
		// Check if process was killed by SIGKILL (expected)
		if isKilledBySignal(err) {
			logrus.Debug("process successfully killed by SIGKILL")

			return commonerrors.ErrKilled
		}

		// Check if process was terminated by SIGTERM (also fine - means it died)
		if isTerminatedBySignal(err) {
			logrus.Debug("process was terminated by SIGTERM during force kill")

			return commonerrors.ErrKilled
		}

		// If harmless error, treat as successful kill
		if isHarmlessWaitError(err) {
			return commonerrors.ErrKilled
		}

		return ctxerrors.Wrap(err, "process failed after SIGKILL")
	}

	// Process exited normally after SIGKILL
	// (shouldn't happen but handle gracefully)
	return commonerrors.ErrKilled
}
