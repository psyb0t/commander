package commander

import (
	"io"
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
