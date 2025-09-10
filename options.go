package commander

import (
	"io"
	"syscall"
	"time"
)

// Option configures command execution
type Option func(*Options)

// Options for configuring command execution
type Options struct {
	Stdin   io.Reader
	Env     []string
	Dir     string
	Timeout *time.Duration
}

// StopOption configures process stop behavior
type StopOption func(*StopOptions)

// StopOptions for configuring process termination
type StopOptions struct {
	Signal syscall.Signal
}

// Option functions using Go idiom
func WithStdin(stdin io.Reader) Option {
	return func(o *Options) {
		o.Stdin = stdin
	}
}

func WithEnv(env []string) Option {
	return func(o *Options) {
		o.Env = env
	}
}

func WithDir(dir string) Option {
	return func(o *Options) {
		o.Dir = dir
	}
}

func WithTimeout(timeout time.Duration) Option {
	return func(o *Options) {
		o.Timeout = &timeout
	}
}

// WithSignal sets the signal to use for graceful termination
func WithSignal(signal syscall.Signal) StopOption {
	return func(o *StopOptions) {
		o.Signal = signal
	}
}
