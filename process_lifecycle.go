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
			logrus.Debugf("stdout reader stopping after %d lines (process done)", lineCount)

			return
		case p.internalStdout <- line:
			logrus.Debugf("stdout line %d: %s", lineCount, line)
		}
	}

	if err := scanner.Err(); err != nil {
		logrus.Debugf("stdout scanner error after %d lines: %v", lineCount, err)
	} else {
		logrus.Debugf("stdout reader finished successfully after %d lines", lineCount)
	}
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
			logrus.Debugf("stderr reader stopping after %d lines (process done)", lineCount)

			return
		case p.internalStderr <- line:
			logrus.Debugf("stderr line %d: %s", lineCount, line)
		}
	}

	if err := scanner.Err(); err != nil {
		logrus.Debugf("stderr scanner error after %d lines: %v", lineCount, err)
	} else {
		logrus.Debugf("stderr reader finished successfully after %d lines", lineCount)
	}
}

// Stop terminates the process gracefully with timeout, then kills forcefully
func (p *process) Stop(ctx context.Context, timeout time.Duration) error {
	var stopErr error

	// Use sync.Once to ensure all cleanup happens exactly once
	p.stopOnce.Do(func() {
		logrus.Debug("performing master stop and cleanup")

		// Step 1: Stop/kill the actual process if it's running
		if p.cmd.Process != nil {
			logrus.Debugf("stopping process PID %d with timeout %v", p.cmd.Process.Pid, timeout)
			stopErr = p.killProcess(ctx, timeout)
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

// killProcess handles the actual process termination with timeout
func (p *process) killProcess(ctx context.Context, timeout time.Duration) error {
	// If no timeout specified, skip graceful termination and force kill immediately
	if timeout <= 0 {
		logrus.Debugf("no timeout specified for process PID %d, force killing immediately", p.cmd.Process.Pid)

		return p.forceKill()
	}

	// First try graceful termination (SIGTERM)
	logrus.Debugf("attempting graceful termination (SIGTERM) for process PID %d", p.cmd.Process.Pid)

	if err := p.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		logrus.Debugf("failed to send SIGTERM to process PID %d, forcing kill: %v", p.cmd.Process.Pid, err)
		// If we can't signal, try killing immediately
		return p.forceKill()
	}

	// Wait for graceful shutdown or force kill after timeout
	logrus.Debugf("SIGTERM sent to process PID %d, waiting %v for graceful shutdown", p.cmd.Process.Pid, timeout)

	// Create a timeout context
	stopCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Wait for either graceful shutdown or timeout
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

		// Check if process was terminated by SIGTERM (our signal)
		if isTerminatedBySignal(err) {
			logrus.Debug("process gracefully terminated by SIGTERM")

			return commonerrors.ErrTerminated
		}

		// Check if process was killed by SIGKILL
		if isKilledBySignal(err) {
			logrus.Debug("process was killed by SIGKILL")

			return commonerrors.ErrKilled
		}

		return err

	case <-stopCtx.Done():
		// Timeout reached, force kill
		logrus.Debugf("graceful shutdown timeout reached for process PID %d, force killing", p.cmd.Process.Pid)

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

		logrus.Debugf("failed to force kill process PID %d: %v", p.cmd.Process.Pid, err)

		return ctxerrors.Wrap(err, "failed to force kill process")
	}

	logrus.Debugf("SIGKILL sent to process PID %d, waiting for process to exit", p.cmd.Process.Pid)

	// Wait for process to exit after SIGKILL
	err := p.cmdWait()
	if err != nil {
		// Check if process was killed by SIGKILL (expected)
		if isKilledBySignal(err) {
			logrus.Debug("process successfully killed by SIGKILL")

			return commonerrors.ErrKilled
		}

		// If harmless error, treat as successful kill
		if isHarmlessWaitError(err) {
			return commonerrors.ErrKilled
		}

		return ctxerrors.Wrap(err, "process failed after SIGKILL")
	}

	// Process exited normally after SIGKILL (shouldn't happen but handle gracefully)
	return commonerrors.ErrKilled
}
