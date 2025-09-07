package commander

import (
	"github.com/sirupsen/logrus"
)

// Stream sends live output to separate stdout and stderr channels
func (p *process) Stream(stdout, stderr chan<- string) {
	p.streamMu.Lock()
	defer p.streamMu.Unlock()

	// Add channels to the list of active streams
	p.streamChans = append(p.streamChans, streamChannels{
		stdout: stdout,
		stderr: stderr,
	})

	logrus.Debugf("added new stream channels - total active streams: %d", len(p.streamChans))
}

// discardInternalOutput continuously drains internal channels to prevent blocking
// Only sends to user channels if they exist, otherwise discards everything
func (p *process) discardInternalOutput() {
	logrus.Debug("starting output discard goroutine")

	defer func() {
		logrus.Debug("output discard goroutine finishing, closing stream channels")
		p.closeStreamChannels()
	}()

	stdoutCount := 0
	stderrCount := 0

	for {
		select {
		case <-p.doneCh:
			logrus.Debugf("output discard goroutine stopping - processed %d stdout, %d stderr lines", stdoutCount, stderrCount)

			return

		case line, ok := <-p.internalStdout:
			if !ok {
				logrus.Debugf("stdout channel closed after %d lines, draining stderr", stdoutCount)
				p.drainStderr()

				return
			}

			stdoutCount++

			p.broadcastToStdout(line)

		case line, ok := <-p.internalStderr:
			if !ok {
				logrus.Debugf("stderr channel closed after %d lines", stderrCount)

				continue
			}

			stderrCount++

			p.broadcastToStderr(line)
		}
	}
}

// drainStderr drains remaining stderr after stdout closes
func (p *process) drainStderr() {
	for {
		select {
		case <-p.doneCh:
			return
		case _, ok := <-p.internalStderr:
			if !ok {
				return
			}
		}
	}
}

// broadcastToStdout sends line to stdout channels if they exist
func (p *process) broadcastToStdout(line string) {
	p.streamMu.Lock()
	defer p.streamMu.Unlock()

	if len(p.streamChans) == 0 {
		return
	}

	// Send to all stdout channels
	for i := len(p.streamChans) - 1; i >= 0; i-- {
		channels := p.streamChans[i]
		if channels.stdout != nil {
			select {
			case channels.stdout <- line:
				// Successfully sent
			default:
				// Channel is blocked or closed, mark stdout as nil
				p.streamChans[i].stdout = nil
			}
		}
	}
}

// broadcastToStderr sends line to stderr channels if they exist
func (p *process) broadcastToStderr(line string) {
	p.streamMu.Lock()
	defer p.streamMu.Unlock()

	if len(p.streamChans) == 0 {
		return
	}

	// Send to all stderr channels
	for i := len(p.streamChans) - 1; i >= 0; i-- {
		channels := p.streamChans[i]
		if channels.stderr != nil {
			select {
			case channels.stderr <- line:
				// Successfully sent
			default:
				// Channel is blocked or closed, mark stderr as nil
				p.streamChans[i].stderr = nil
			}
		}
	}
}

// closeStreamChannels closes all active stream channels
func (p *process) closeStreamChannels() {
	p.streamMu.Lock()
	defer p.streamMu.Unlock()

	for _, channels := range p.streamChans {
		if channels.stdout != nil {
			close(channels.stdout)
		}

		if channels.stderr != nil {
			close(channels.stderr)
		}
	}

	p.streamChans = nil
}
