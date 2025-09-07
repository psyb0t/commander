package commander

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestWithStdin(t *testing.T) {
	stdin := strings.NewReader("test input")
	opts := &Options{}

	WithStdin(stdin)(opts)

	assert.Equal(t, stdin, opts.Stdin)
}

func TestWithEnv(t *testing.T) {
	env := []string{"KEY1=value1", "KEY2=value2"}
	opts := &Options{}

	WithEnv(env)(opts)

	assert.Equal(t, env, opts.Env)
}

func TestWithDir(t *testing.T) {
	dir := "/tmp/test"
	opts := &Options{}

	WithDir(dir)(opts)

	assert.Equal(t, dir, opts.Dir)
}

func TestWithTimeout(t *testing.T) {
	timeout := 5 * time.Second
	opts := &Options{}

	WithTimeout(timeout)(opts)

	assert.NotNil(t, opts.Timeout)
	assert.Equal(t, timeout, *opts.Timeout)
}

func TestMultipleOptions(t *testing.T) {
	stdin := strings.NewReader("input")
	env := []string{"TEST=true"}
	dir := "/test/dir"
	timeout := 10 * time.Second

	opts := &Options{}

	WithStdin(stdin)(opts)
	WithEnv(env)(opts)
	WithDir(dir)(opts)
	WithTimeout(timeout)(opts)

	assert.Equal(t, stdin, opts.Stdin)
	assert.Equal(t, env, opts.Env)
	assert.Equal(t, dir, opts.Dir)
	assert.NotNil(t, opts.Timeout)
	assert.Equal(t, timeout, *opts.Timeout)
}
