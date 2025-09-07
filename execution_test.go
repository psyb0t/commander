package commander

import (
	"context"
	"errors"
	"testing"
	"time"

	commonerrors "github.com/psyb0t/common-go/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExecutionContext_HandleExecutionError(t *testing.T) {
	tests := []struct {
		name           string
		setupContext   func() *executionContext
		inputError     error
		expectedResult error
	}{
		{
			name: "nil error returns nil",
			setupContext: func() *executionContext {
				ctx := context.Background()
				return &executionContext{
					ctx:        ctx,
					timeoutCtx: ctx,
					name:       "test",
					args:       []string{"arg"},
				}
			},
			inputError:     nil,
			expectedResult: nil,
		},
		{
			name: "timeout error returns ErrTimeout",
			setupContext: func() *executionContext {
				ctx := context.Background()
				timeoutCtx, cancel := context.WithTimeout(ctx, 1*time.Nanosecond)
				cancel()            // Force timeout
				<-timeoutCtx.Done() // Wait for timeout

				return &executionContext{
					ctx:        ctx,
					timeoutCtx: timeoutCtx,
					name:       "test",
					args:       []string{"arg"},
				}
			},
			inputError:     errors.New("some error"),
			expectedResult: commonerrors.ErrTimeout,
		},
		{
			name: "regular error gets wrapped",
			setupContext: func() *executionContext {
				ctx := context.Background()
				return &executionContext{
					ctx:        ctx,
					timeoutCtx: ctx,
					name:       "test",
					args:       []string{"arg"},
				}
			},
			inputError:     errors.New("command failed"),
			expectedResult: errors.New("wrapped error"), // Will check error wrapping
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ec := tt.setupContext()
			result := ec.handleExecutionError(tt.inputError)

			if tt.expectedResult == nil {
				assert.NoError(t, result)
				return
			}

			if tt.expectedResult == commonerrors.ErrTimeout {
				assert.Equal(t, commonerrors.ErrTimeout, result)
				return
			}

			// For wrapped errors, just check that we got an error back
			assert.Error(t, result)
			assert.Contains(t, result.Error(), "command failed")
		})
	}
}

func TestExecutionContext_Cleanup(t *testing.T) {
	t.Run("cleanup calls cancel function", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())

		ec := &executionContext{
			ctx:    ctx,
			cancel: cancel,
		}

		// Context should not be cancelled initially
		assert.NoError(t, ctx.Err())

		ec.cleanup()

		// Context should be cancelled after cleanup
		assert.Error(t, ctx.Err())
		assert.Equal(t, context.Canceled, ctx.Err())
	})

	t.Run("cleanup with nil cancel does not panic", func(t *testing.T) {
		ec := &executionContext{
			cancel: nil,
		}

		// Should not panic
		assert.NotPanics(t, func() {
			ec.cleanup()
		})
	})
}

func TestNewExecutionContext(t *testing.T) {
	cmd := &commander{}
	ctx := context.Background()
	name := "echo"
	args := []string{"test"}
	opts := &Options{
		Timeout: func() *time.Duration { d := 5 * time.Second; return &d }(),
	}

	ec := cmd.newExecutionContext(ctx, name, args, opts)

	require.NotNil(t, ec)
	assert.Equal(t, ctx, ec.ctx)
	assert.Equal(t, name, ec.name)
	assert.Equal(t, args, ec.args)
	assert.NotNil(t, ec.cancel)
	assert.NotNil(t, ec.cmd)
	assert.NotNil(t, ec.timeoutCtx)

	// Cleanup to avoid resource leaks in tests
	ec.cleanup()
}

func TestSignalDetection(t *testing.T) {
	t.Run("normal_exit_no_signal", func(t *testing.T) {
		// Test normal command that exits without signals
		commander := New()
		ctx := context.Background()

		err := commander.Run(ctx, "echo", []string{"hello"})
		assert.NoError(t, err, "Normal command should succeed")

		// Should not be any signal errors
		assert.NotErrorIs(t, err, commonerrors.ErrTerminated, "Should not be ErrTerminated")
		assert.NotErrorIs(t, err, commonerrors.ErrKilled, "Should not be ErrKilled")
		assert.NotErrorIs(t, err, commonerrors.ErrTimeout, "Should not be ErrTimeout")
	})

	t.Run("command_not_found", func(t *testing.T) {
		// Test command that doesn't exist
		commander := New()
		ctx := context.Background()

		err := commander.Run(ctx, "nonexistent-command-12345", []string{})

		// Should get an error but not signal-related
		assert.Error(t, err, "Non-existent command should fail")
		assert.NotErrorIs(t, err, commonerrors.ErrTerminated, "Should not be ErrTerminated")
		assert.NotErrorIs(t, err, commonerrors.ErrKilled, "Should not be ErrKilled")
		assert.NotErrorIs(t, err, commonerrors.ErrTimeout, "Should not be ErrTimeout")
	})

	t.Run("timeout_detection", func(t *testing.T) {
		// Test that timeout is properly detected
		commander := New()
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()

		err := commander.Run(ctx, "sleep", []string{"1"})

		// Should get timeout error
		assert.Error(t, err, "Command should timeout")
		assert.ErrorIs(t, err, commonerrors.ErrTimeout, "Should be ErrTimeout")
		assert.NotErrorIs(t, err, commonerrors.ErrTerminated, "Should not be ErrTerminated")
		assert.NotErrorIs(t, err, commonerrors.ErrKilled, "Should not be ErrKilled")
	})
}
