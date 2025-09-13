package commander

import (
	"strings"
	"testing"

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


func TestMultipleOptions(t *testing.T) {
	stdin := strings.NewReader("input")
	env := []string{"TEST=true"}
	dir := "/test/dir"

	opts := &Options{}

	WithStdin(stdin)(opts)
	WithEnv(env)(opts)
	WithDir(dir)(opts)

	assert.Equal(t, stdin, opts.Stdin)
	assert.Equal(t, env, opts.Env)
	assert.Equal(t, dir, opts.Dir)
}
