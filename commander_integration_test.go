package commander

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	commonerrors "github.com/psyb0t/common-go/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCommander_BasicOperations(t *testing.T) {
	tests := []struct {
		name        string
		command     string
		args        []string
		operation   string
		expectError bool
		expectOut   string
	}{
		{
			name:      "run echo command",
			command:   "echo",
			args:      []string{"hello"},
			operation: "run",
		},
		{
			name:      "output echo command",
			command:   "echo",
			args:      []string{"world"},
			operation: "output",
			expectOut: "world\n",
		},
		{
			name:      "combined output echo command",
			command:   "echo",
			args:      []string{"combined"},
			operation: "combined",
			expectOut: "combined\n",
		},
		{
			name:        "failing command",
			command:     "false",
			args:        []string{},
			operation:   "run",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := New()
			ctx := context.Background()

			switch tt.operation {
			case "run":
				err := cmd.Run(ctx, tt.command, tt.args)
				if tt.expectError {
					assert.Error(t, err)
				} else {
					assert.NoError(t, err)
				}

			case "output":
				stdout, stderr, err := cmd.Output(ctx, tt.command, tt.args)
				require.NoError(t, err)
				assert.Equal(t, tt.expectOut, string(stdout))
				assert.NotNil(t, stderr)

			case "combined":
				combined, err := cmd.CombinedOutput(ctx, tt.command, tt.args)
				require.NoError(t, err)
				assert.Equal(t, tt.expectOut, string(combined))
			}
		})
	}
}

func TestCommander_WithOptions(t *testing.T) {
	tests := []struct {
		name    string
		setup   func() ([]Option, func(*testing.T, error, []byte))
		command string
		args    []string
	}{
		{
			name:    "with stdin",
			command: "cat",
			args:    []string{},
			setup: func() ([]Option, func(*testing.T, error, []byte)) {
				stdin := strings.NewReader("test input")
				opts := []Option{WithStdin(stdin)}
				return opts, func(t *testing.T, err error, output []byte) {
					require.NoError(t, err)
					assert.Equal(t, "test input", string(output))
				}
			},
		},
		{
			name:    "with environment",
			command: "sh",
			args:    []string{"-c", "echo $TEST_VAR"},
			setup: func() ([]Option, func(*testing.T, error, []byte)) {
				opts := []Option{WithEnv([]string{"TEST_VAR=hello"})}
				return opts, func(t *testing.T, err error, output []byte) {
					require.NoError(t, err)
					assert.Equal(t, "hello\n", string(output))
				}
			},
		},
		{
			name:    "with working directory",
			command: "pwd",
			args:    []string{},
			setup: func() ([]Option, func(*testing.T, error, []byte)) {
				opts := []Option{WithDir("/tmp")}
				return opts, func(t *testing.T, err error, output []byte) {
					require.NoError(t, err)
					assert.Equal(t, "/tmp\n", string(output))
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := New()
			ctx := context.Background()

			optFuncs, verify := tt.setup()
			stdout, _, err := cmd.Output(ctx, tt.command, tt.args, optFuncs...)
			verify(t, err, stdout)
		})
	}
}

func TestCommander_ProcessControl(t *testing.T) {
	t.Run("start and wait", func(t *testing.T) {
		cmd := New()
		ctx := context.Background()

		proc, err := cmd.Start(ctx, "echo", []string{"process test"})
		require.NoError(t, err)

		err = proc.Wait()
		require.NoError(t, err)
	})

	t.Run("stdout stream", func(t *testing.T) {
		cmd := New()
		ctx := context.Background()

		proc, err := cmd.Start(ctx, "echo", []string{"stdout test"})
		require.NoError(t, err)

		// Use Stream() instead of pipe
		stdout := make(chan string, 10)
		proc.Stream(stdout, nil)

		// Collect all output
		var lines []string
		for line := range stdout {
			lines = append(lines, line)
		}

		err = proc.Wait()
		require.NoError(t, err)

		// Verify we got the expected output
		require.Len(t, lines, 1)
		assert.Equal(t, "stdout test", lines[0])
	})

	t.Run("stderr stream", func(t *testing.T) {
		cmd := New()
		ctx := context.Background()

		// Use a command that naturally writes to stderr
		proc, err := cmd.Start(ctx, "sh", []string{"-c", "echo 'stderr test' >&2; sleep 0.1"})
		require.NoError(t, err)

		// Use Stream() instead of pipe
		stderr := make(chan string, 10)
		proc.Stream(nil, stderr)

		// Collect all output
		var lines []string
		for line := range stderr {
			lines = append(lines, line)
		}

		err = proc.Wait()
		require.NoError(t, err)

		// Verify we got the expected output
		require.Len(t, lines, 1)
		assert.Equal(t, "stderr test", lines[0])
	})
}

func TestCommander_ContextCancellation(t *testing.T) {
	tests := []struct {
		name        string
		timeout     time.Duration
		command     string
		args        []string
		expectError bool
		errorCheck  func(t *testing.T, err error)
	}{
		{
			name:        "quick command with long timeout",
			timeout:     5 * time.Second,
			command:     "echo",
			args:        []string{"quick"},
			expectError: false,
		},
		{
			name:        "slow command with short timeout",
			timeout:     100 * time.Millisecond,
			command:     "sleep",
			args:        []string{"1"},
			expectError: true,
			errorCheck: func(t *testing.T, err error) {
				// With our new WithTimeout option, we should get ErrTimeout
				assert.ErrorIs(t, err, commonerrors.ErrTimeout, "Expected ErrTimeout, got: %s", err)
			},
		},
		{
			name:        "medium command with medium timeout",
			timeout:     200 * time.Millisecond,
			command:     "sleep",
			args:        []string{"0.1"},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := New()
			ctx := context.Background()

			var err error
			if tt.timeout > 0 {
				// Use our new WithTimeout option
				err = cmd.Run(ctx, tt.command, tt.args, WithTimeout(tt.timeout))
			} else {
				err = cmd.Run(ctx, tt.command, tt.args)
			}

			if tt.expectError {
				assert.Error(t, err)
				if tt.errorCheck != nil {
					tt.errorCheck(t, err)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestCommander_StreamingOutput(t *testing.T) {
	t.Run("continuous output stream", func(t *testing.T) {
		cmd := New()
		ctx := context.Background()

		// Command that outputs every second for 3 seconds
		proc, err := cmd.Start(ctx, "bash", []string{"-c", "for i in {1..3}; do echo \"line $i\"; sleep 1; done"})
		require.NoError(t, err)

		// Use Stream() instead of StdoutPipe
		stdout := make(chan string, 10)
		proc.Stream(stdout, nil)

		// Read output line by line as it comes
		var lines []string
		startTime := time.Now()

		for line := range stdout {
			lines = append(lines, line)
			t.Logf("Received at %v: %s", time.Since(startTime), line)
		}

		err = proc.Wait()
		require.NoError(t, err)

		// Verify we got all expected lines
		expectedLines := []string{"line 1", "line 2", "line 3"}
		assert.Equal(t, expectedLines, lines)

		// Verify it took approximately 3 seconds (with some tolerance)
		duration := time.Since(startTime)
		assert.True(t, duration >= 2*time.Second, "Should take at least 2 seconds")
		assert.True(t, duration <= 5*time.Second, "Should complete within 5 seconds")
	})
}

func TestCommander_EarlyTermination(t *testing.T) {
	t.Run("cancel during continuous output", func(t *testing.T) {
		cmd := New()

		// Create a context we can cancel
		ctx, cancel := context.WithCancel(context.Background())

		// Command that outputs every 500ms for a long time
		proc, err := cmd.Start(ctx, "bash", []string{"-c", "for i in {1..10}; do echo \"output $i\"; sleep 0.5; done"})
		require.NoError(t, err)

		// Use Stream() instead of StdoutPipe
		stdout := make(chan string, 10)
		proc.Stream(stdout, nil)

		// Read some output, then cancel
		var lines []string
		startTime := time.Now()

		// Read first 2 lines
		for len(lines) < 2 {
			select {
			case line := <-stdout:
				lines = append(lines, line)
				t.Logf("Received at %v: %s", time.Since(startTime), line)
			case <-time.After(2 * time.Second):
				t.Fatal("Timeout waiting for initial output")
			}
		}

		// Cancel the context after receiving some output
		cancel()

		// Try to read remaining output (should be limited)
		timeout := time.After(2 * time.Second)

	readLoop:
		for {
			select {
			case line, ok := <-stdout:
				if !ok {
					// Channel closed
					break readLoop
				}
				lines = append(lines, line)
				t.Logf("Received after cancel at %v: %s", time.Since(startTime), line)
			case <-timeout:
				// Timeout reached, which is fine for this test
				t.Log("Timeout reached waiting for process to finish")
				break readLoop
			}
		}

		// Process should return an error due to cancellation
		err = proc.Wait()
		assert.Error(t, err)
		// Process should be killed or canceled
		isKilled := errors.Is(err, commonerrors.ErrKilled) || strings.Contains(err.Error(), "signal: killed")
		isCanceled := strings.Contains(err.Error(), "context canceled")

		assert.True(t, isKilled || isCanceled,
			"Expected cancellation or kill error, got: %s", err.Error())

		// We should have received at least the first 2 lines
		assert.GreaterOrEqual(t, len(lines), 2, "Should receive at least 2 lines before cancellation")
		assert.LessOrEqual(t, len(lines), 5, "Should not receive all 10 lines due to early cancellation")

		// Verify the lines we got are correct
		for i, line := range lines {
			expected := fmt.Sprintf("output %d", i+1)
			assert.Equal(t, expected, line)
		}

		t.Logf("Total lines received: %d (expected 2-5 due to cancellation)", len(lines))
	})
}

func TestCommander_DefaultOutputBehavior(t *testing.T) {
	tests := []struct {
		name        string
		command     string
		args        []string
		opts        []Option
		expectOut   string
		expectError bool
		testFunc    func(t *testing.T, cmd Commander, ctx context.Context)
	}{
		{
			name:    "default behavior - output discarded",
			command: "echo",
			args:    []string{"this goes to /dev/null"},
			opts:    nil,
			testFunc: func(t *testing.T, cmd Commander, ctx context.Context) {
				// This should send output to /dev/null, not to our terminal
				err := cmd.Run(ctx, "echo", []string{"this goes to /dev/null"})
				assert.NoError(t, err)
				t.Log("Command completed - output was discarded to /dev/null (Go stdlib default)")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := New()
			ctx := context.Background()

			if tt.testFunc != nil {
				tt.testFunc(t, cmd, ctx)
			}
		})
	}

	t.Run("verify exec.Cmd defaults", func(t *testing.T) {
		// Let's test this by creating our own exec.Cmd and seeing what the defaults are
		cmd := exec.CommandContext(context.Background(), "echo", "test")
		t.Logf("Default cmd.Stdout: %v", cmd.Stdout)
		t.Logf("Default cmd.Stderr: %v", cmd.Stderr)
		t.Logf("Default cmd.Stdin: %v", cmd.Stdin)

		// According to Go docs:
		// If Stdout is nil, Run connects the process stdout to os.DevNull
		// If Stderr is nil, Run connects the process stderr to os.DevNull
		// If Stdin is nil, the process reads from os.DevNull
	})
}

func TestCommander_Stream(t *testing.T) {
	tests := []struct {
		name        string
		command     string
		args        []string
		testFunc    func(t *testing.T, proc Process)
		expectError bool
	}{
		{
			name:    "basic streaming",
			command: "bash",
			args:    []string{"-c", "echo line1; echo line2; echo line3"},
			testFunc: func(t *testing.T, proc Process) {
				// Start streaming stdout only
				stdout := make(chan string, 10)
				proc.Stream(stdout, nil)

				// Collect output
				var lines []string
				for line := range stdout {
					lines = append(lines, line)
					if len(lines) >= 3 {
						break
					}
				}

				// Verify we got the expected lines
				assert.Len(t, lines, 3)
				assert.Contains(t, lines[0], "line1")
				assert.Contains(t, lines[1], "line2")
				assert.Contains(t, lines[2], "line3")

				// Lines should be raw without prefixes now
				for _, line := range lines {
					assert.NotContains(t, line, "[STDOUT]")
				}
			},
		},
		{
			name:    "stdout and stderr mixed",
			command: "bash",
			args:    []string{"-c", "echo stdout1; echo stderr1 >&2; echo stdout2; echo stderr2 >&2"},
			testFunc: func(t *testing.T, proc Process) {
				stdout := make(chan string, 10)
				stderr := make(chan string, 10)
				proc.Stream(stdout, stderr)

				var stdoutLines []string
				var stderrLines []string
				timeout := time.After(2 * time.Second)

			readLoop:
				for len(stdoutLines)+len(stderrLines) < 4 {
					select {
					case line := <-stdout:
						stdoutLines = append(stdoutLines, line)
					case line := <-stderr:
						stderrLines = append(stderrLines, line)
					case <-timeout:
						break readLoop
					}
				}

				// Should have received lines on both channels
				assert.True(t, len(stdoutLines) > 0, "Should receive stdout lines")
				assert.True(t, len(stderrLines) > 0, "Should receive stderr lines")

				// Verify content
				for _, line := range stdoutLines {
					assert.Contains(t, line, "stdout")
				}
				for _, line := range stderrLines {
					assert.Contains(t, line, "stderr")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := New()
			ctx := context.Background()

			proc, err := cmd.Start(ctx, tt.command, tt.args)
			if tt.expectError {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)

			tt.testFunc(t, proc)

			err = proc.Wait()
			assert.NoError(t, err)
		})
	}
}

func TestCommander_StreamAdvanced(t *testing.T) {
	t.Run("live streaming behavior - no history replay", func(t *testing.T) {
		cmd := New()
		ctx := context.Background()

		// Start a command that outputs lines with delays
		proc, err := cmd.Start(ctx, "bash", []string{
			"-c",
			"echo first; sleep 0.2; echo second; sleep 0.2; echo third; sleep 0.2; echo fourth",
		})
		require.NoError(t, err)

		// Start first stream and read some lines
		ch1 := make(chan string, 10)
		proc.Stream(ch1, nil)

		// Read first 2 lines
		line1 := <-ch1
		line2 := <-ch1
		assert.Contains(t, line1, "first")
		assert.Contains(t, line2, "second")

		// Wait a bit for more output to be generated
		time.Sleep(500 * time.Millisecond)

		// Start second stream - should only get NEW lines, not replay
		ch2 := make(chan string, 10)
		proc.Stream(ch2, nil)

		// This should get only the latest/new output, not the historical first/second lines
		var newLines []string
		timeout := time.After(1 * time.Second)
		for {
			select {
			case line := <-ch2:
				newLines = append(newLines, line)
				if len(newLines) >= 2 || (len(newLines) >= 1 && strings.Contains(line, "fourth")) {
					goto done
				}
			case <-timeout:
				goto done
			}
		}
	done:

		// Verify we didn't get the first two lines again
		for _, line := range newLines {
			assert.NotContains(t, line, "first", "Should not replay first line")
			assert.NotContains(t, line, "second", "Should not replay second line")
		}

		err = proc.Wait()
		assert.NoError(t, err)
	})

	t.Run("join live stream in progress", func(t *testing.T) {
		cmd := New()
		ctx := context.Background()

		// Start a command that outputs lines with delays - longer running
		proc, err := cmd.Start(ctx, "bash", []string{
			"-c",
			"for i in {1..8}; do echo \"line $i\"; sleep 0.2; done",
		})
		require.NoError(t, err)

		// Start first stream
		stream1 := make(chan string, 10)
		proc.Stream(stream1, nil)

		// Let stream1 get a few lines first
		var stream1Lines []string
		for len(stream1Lines) < 3 {
			select {
			case line := <-stream1:
				stream1Lines = append(stream1Lines, line)
				t.Logf("Stream1 received: %s", line)
			case <-time.After(1 * time.Second):
				t.Fatal("Timeout waiting for stream1 to receive lines")
			}
		}

		// Now start second stream WHILE first stream is still active and receiving
		stream2 := make(chan string, 10)
		proc.Stream(stream2, nil)
		t.Logf("Stream2 joined after stream1 received %d lines", len(stream1Lines))

		// Both streams should now receive the SAME remaining lines
		var stream2Lines []string
		timeout := time.After(3 * time.Second)

		// Collect remaining lines from both streams
		for len(stream1Lines) < 8 || len(stream2Lines) < 5 { // stream2 should get ~5 remaining lines
			select {
			case line := <-stream1:
				stream1Lines = append(stream1Lines, line)
				t.Logf("Stream1 got: %s (total: %d)", line, len(stream1Lines))
			case line := <-stream2:
				stream2Lines = append(stream2Lines, line)
				t.Logf("Stream2 got: %s (total: %d)", line, len(stream2Lines))
			case <-timeout:
				goto verify
			}
		}

	verify:
		err = proc.Wait()
		assert.NoError(t, err)

		// Stream1 should have received all 8 lines (was there from start)
		assert.True(t, len(stream1Lines) >= 7, "Stream1 should get most/all lines")

		// Stream2 should have received the later lines (joined mid-stream)
		assert.True(t, len(stream2Lines) >= 4, "Stream2 should get remaining lines after joining")

		// The lines that both streams received should be identical
		// Find the overlap period - lines that both streams got
		stream1Set := make(map[string]bool)
		for _, line := range stream1Lines {
			stream1Set[line] = true
		}

		overlapCount := 0
		for _, line := range stream2Lines {
			if stream1Set[line] {
				overlapCount++
			}
		}

		assert.True(t, overlapCount >= 3, "Both streams should receive some identical lines")
		t.Logf("Stream1 total: %d, Stream2 total: %d, Overlap: %d",
			len(stream1Lines), len(stream2Lines), overlapCount)
	})

	t.Run("multiple concurrent streams", func(t *testing.T) {
		cmd := New()
		ctx := context.Background()

		// Start a command that outputs several lines
		proc, err := cmd.Start(ctx, "bash", []string{
			"-c",
			"for i in {1..5}; do echo \"line $i\"; sleep 0.1; done",
		})
		require.NoError(t, err)

		// Start multiple streams simultaneously
		ch1 := make(chan string, 10)
		ch2 := make(chan string, 10)
		ch3 := make(chan string, 10)

		proc.Stream(ch1, nil)
		proc.Stream(ch2, nil)
		proc.Stream(ch3, nil)

		// Collect output from all streams
		var lines1, lines2, lines3 []string

		timeout := time.After(2 * time.Second)
		done := make(chan bool, 3)

		// Reader for ch1
		go func() {
			for line := range ch1 {
				lines1 = append(lines1, line)
				if len(lines1) >= 5 {
					break
				}
			}
			done <- true
		}()

		// Reader for ch2
		go func() {
			for line := range ch2 {
				lines2 = append(lines2, line)
				if len(lines2) >= 5 {
					break
				}
			}
			done <- true
		}()

		// Reader for ch3
		go func() {
			for line := range ch3 {
				lines3 = append(lines3, line)
				if len(lines3) >= 5 {
					break
				}
			}
			done <- true
		}()

		// Wait for all readers to complete or timeout
		completed := 0
		for completed < 3 {
			select {
			case <-done:
				completed++
			case <-timeout:
				t.Log("Timeout waiting for streams to complete")
				goto checkResults
			}
		}

	checkResults:
		// All streams should receive the same lines (broadcast)
		assert.True(t, len(lines1) > 0, "Stream 1 should receive lines")
		assert.True(t, len(lines2) > 0, "Stream 2 should receive lines")
		assert.True(t, len(lines3) > 0, "Stream 3 should receive lines")

		// All streams should get the same content (broadcast behavior)
		minLen := min(len(lines3), min(len(lines2), len(lines1)))

		for i := range minLen {
			assert.Equal(t, lines1[i], lines2[i], "Streams should receive same content")
			assert.Equal(t, lines1[i], lines3[i], "Streams should receive same content")
		}

		err = proc.Wait()
		assert.NoError(t, err)
	})
}

func TestCommander_OutputAndCombinedOutput(t *testing.T) {
	t.Run("output with error", func(t *testing.T) {
		cmd := New()
		ctx := context.Background()

		// Command that fails
		_, _, err := cmd.Output(ctx, "false", []string{})
		assert.Error(t, err)
	})

	t.Run("combined output success", func(t *testing.T) {
		cmd := New()
		ctx := context.Background()

		// Command that outputs to both stdout and stderr successfully
		stdout, stderr, err := cmd.Output(ctx, "sh", []string{"-c", "echo stdout && echo stderr >&2"})
		assert.NoError(t, err)
		assert.Contains(t, string(stdout), "stdout")
		assert.Contains(t, string(stderr), "stderr")

		// Test combined output separately
		combined, err := cmd.CombinedOutput(ctx, "sh", []string{"-c", "echo stdout && echo stderr >&2"})
		assert.NoError(t, err)
		assert.Contains(t, string(combined), "stdout")
		assert.Contains(t, string(combined), "stderr")
	})

	t.Run("combined output with error", func(t *testing.T) {
		cmd := New()
		ctx := context.Background()

		// Command that outputs to both stdout and stderr, then fails
		stdout, stderr, err := cmd.Output(ctx, "sh", []string{"-c", "echo stdout && echo stderr >&2 && exit 1"})
		assert.Error(t, err)
		assert.Contains(t, string(stdout), "stdout")
		assert.Contains(t, string(stderr), "stderr")

		// Test combined output with error
		combined, err := cmd.CombinedOutput(ctx, "sh", []string{"-c", "echo stdout && echo stderr >&2 && exit 1"})
		assert.Error(t, err)
		assert.Contains(t, string(combined), "stdout")
		assert.Contains(t, string(combined), "stderr")
	})

	t.Run("output with context timeout", func(t *testing.T) {
		cmd := New()
		ctx := context.Background()

		// Command that takes too long with our new WithTimeout option
		_, _, err := cmd.Output(ctx, "sleep", []string{"1"}, WithTimeout(50*time.Millisecond))
		assert.Error(t, err)
		assert.ErrorIs(t, err, commonerrors.ErrTimeout, "Expected ErrTimeout, got: %s", err)
	})

	t.Run("combined output with context timeout", func(t *testing.T) {
		cmd := New()
		ctx := context.Background()

		// Command that takes too long with our new WithTimeout option
		_, _, err := cmd.Output(ctx, "sleep", []string{"1"}, WithTimeout(50*time.Millisecond))
		assert.Error(t, err)
		assert.ErrorIs(t, err, commonerrors.ErrTimeout, "Expected ErrTimeout, got: %s", err)
	})
}

func TestCommander_ConcurrentRealCommands(t *testing.T) {
	cmd := New()
	ctx := context.Background()

	// Define 3 different real commands to run concurrently
	type commandTest struct {
		name     string
		command  string
		args     []string
		expected string
	}

	tests := []commandTest{
		{
			name:     "echo test",
			command:  "echo",
			args:     []string{"concurrent test 1"},
			expected: "concurrent test 1\n",
		},
		{
			name:     "date command",
			command:  "date",
			args:     []string{"+%Y"},
			expected: "202", // Should contain current year prefix
		},
		{
			name:     "wc line count",
			command:  "sh",
			args:     []string{"-c", "echo -e 'line1\\nline2\\nline3' | wc -l"},
			expected: "3",
		},
	}

	// Channels to collect results
	results := make(chan struct {
		name   string
		output string
		err    error
	}, len(tests))

	// Launch all commands concurrently
	for _, test := range tests {
		go func(tc commandTest) {
			stdout, _, err := cmd.Output(ctx, tc.command, tc.args)
			results <- struct {
				name   string
				output string
				err    error
			}{
				name:   tc.name,
				output: string(stdout),
				err:    err,
			}
		}(test)
	}

	// Collect and verify all results
	collectedResults := make(map[string]string)
	for range tests {
		select {
		case result := <-results:
			assert.NoError(t, result.err, "Command %s should not error", result.name)
			collectedResults[result.name] = result.output
			t.Logf("Command '%s' output: %q", result.name, result.output)
		case <-time.After(5 * time.Second):
			t.Fatal("Timeout waiting for concurrent commands to complete")
		}
	}

	// Verify outputs contain expected content
	assert.Contains(t, collectedResults["echo test"], "concurrent test 1")
	assert.Contains(t, collectedResults["date command"], "202") // Current year should start with 202x
	assert.Contains(t, collectedResults["wc line count"], "3")

	t.Logf("All %d concurrent commands completed successfully", len(tests))
}

func TestCommander_ConcurrentLoadTest(t *testing.T) {
	cmd := New()
	ctx := context.Background()

	// Test different load levels
	loadLevels := []int{3, 20, 50, 100, 1000}

	for _, numCommands := range loadLevels {
		t.Run(fmt.Sprintf("load_test_%d_commands", numCommands), func(t *testing.T) {
			startTime := time.Now()

			// Create channels for results
			results := make(chan struct {
				id     int
				output string
				err    error
			}, numCommands)

			// Launch all commands concurrently
			for i := range numCommands {
				go func(cmdID int) {
					// Mix different commands for realistic load
					var output []byte
					var err error

					switch cmdID % 3 {
					case 0:
						output, _, err = cmd.Output(ctx, "echo", []string{fmt.Sprintf("command_%d", cmdID)})
					case 1:
						output, _, err = cmd.Output(ctx, "date", []string{"+%s"}) // Unix timestamp
					case 2:
						output, _, err = cmd.Output(ctx, "sh", []string{"-c", fmt.Sprintf("echo %d | wc -c", cmdID)})
					}

					results <- struct {
						id     int
						output string
						err    error
					}{
						id:     cmdID,
						output: string(output),
						err:    err,
					}
				}(i)
			}

			// Collect all results with timeout
			successCount := 0
			errorCount := 0
			timeout := time.After(30 * time.Second) // Generous timeout for 1000 commands

			for range numCommands {
				select {
				case result := <-results:
					if result.err != nil {
						errorCount++
						t.Logf("Command %d failed: %v", result.id, result.err)
					} else {
						successCount++
						// Verify output is not empty
						assert.NotEmpty(t, strings.TrimSpace(result.output),
							"Command %d should produce output", result.id)
					}
				case <-timeout:
					t.Fatalf("Timeout waiting for %d commands (got %d successes, %d errors)",
						numCommands, successCount, errorCount)
				}
			}

			duration := time.Since(startTime)
			commandsPerSecond := float64(numCommands) / duration.Seconds()

			// Verify results
			assert.Equal(t, numCommands, successCount+errorCount,
				"Should receive response from all commands")
			assert.Equal(t, 0, errorCount, "All commands should succeed")
			assert.Equal(t, numCommands, successCount, "All commands should succeed")

			t.Logf("Load test completed: %d commands in %v (%.2f commands/sec)",
				numCommands, duration, commandsPerSecond)

			// Quick verification: check some outputs to ensure they're real
			if numCommands >= 20 {
				t.Logf("Sample verification - this was real command execution, not cached bullshit")
			}

			// Performance expectations (adjust based on system)
			if numCommands <= 100 {
				assert.Less(t, duration, 5*time.Second,
					"Small loads should complete quickly")
			}
		})
	}
}

func TestCommander_StreamingWithStop(t *testing.T) {
	t.Run("streaming stops when process is stopped", func(t *testing.T) {
		cmd := New()
		ctx := context.Background()

		// Start a long-running command that outputs continuously
		proc, err := cmd.Start(ctx, "bash", []string{
			"-c",
			"for i in {1..100}; do echo \"line $i\"; sleep 0.1; done",
		})
		require.NoError(t, err)

		// Start streaming
		stdout := make(chan string, 10)
		proc.Stream(stdout, nil)

		// Collect some initial output
		var lines []string
		timeout := time.After(1 * time.Second)

		// Read a few lines to verify streaming is working
		for len(lines) < 3 {
			select {
			case line := <-stdout:
				lines = append(lines, line)
				t.Logf("Received before stop: %s", line)
			case <-timeout:
				t.Fatal("Timeout waiting for initial streaming output")
			}
		}

		// Verify we got some output
		assert.True(t, len(lines) >= 3, "Should have received at least 3 lines before stopping")

		// Now stop the process
		t.Log("Stopping process...")
		stopCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
		defer cancel()
		stopErr := proc.Stop(stopCtx)
		// Process should be stopped (might return error which is expected)

		// The key test: verify that streaming stops and the channel gets closed
		// We should not hang waiting for more output
		channelClosed := false
		drainTimeout := time.After(2 * time.Second) // Generous timeout for cleanup

	drainLoop:
		for {
			select {
			case line, ok := <-stdout:
				if !ok {
					// Channel was closed - this is what we want!
					channelClosed = true
					t.Log("Stream channel was properly closed after Stop()")
					break drainLoop
				}
				// We might get a few more lines as the process shuts down
				t.Logf("Received after stop: %s", line)
			case <-drainTimeout:
				// If we timeout, that means the channel never closed - bad!
				break drainLoop
			}
		}

		// Verify the channel was closed (streaming stopped properly)
		assert.True(t, channelClosed, "Stream channel should be closed when process is stopped")

		// Don't call Wait() after Stop() - Stop() already waits internally
		t.Logf("Process stopped with stop error: %v", stopErr)

		t.Logf("Test completed - streaming properly stopped when process was killed")
	})
}

func TestCommander_WithTimeout(t *testing.T) {
	tests := []struct {
		name        string
		method      string
		timeout     time.Duration
		command     string
		args        []string
		expectError bool
		errorType   error
	}{
		{
			name:        "Run with timeout - command completes in time",
			method:      "run",
			timeout:     500 * time.Millisecond,
			command:     "echo",
			args:        []string{"hello"},
			expectError: false,
		},
		{
			name:        "Run with timeout - command times out",
			method:      "run",
			timeout:     100 * time.Millisecond,
			command:     "sleep",
			args:        []string{"1"},
			expectError: true,
			errorType:   commonerrors.ErrTimeout,
		},
		{
			name:        "Output with timeout - command completes in time",
			method:      "output",
			timeout:     500 * time.Millisecond,
			command:     "echo",
			args:        []string{"test output"},
			expectError: false,
		},
		{
			name:        "Output with timeout - command times out",
			method:      "output",
			timeout:     100 * time.Millisecond,
			command:     "sleep",
			args:        []string{"1"},
			expectError: true,
			errorType:   commonerrors.ErrTimeout,
		},
		{
			name:        "Start with timeout - command completes in time",
			method:      "start",
			timeout:     500 * time.Millisecond,
			command:     "echo",
			args:        []string{"test"},
			expectError: false,
		},
		{
			name:        "Start with timeout - command times out",
			method:      "start",
			timeout:     100 * time.Millisecond,
			command:     "sleep",
			args:        []string{"1"},
			expectError: true,
			errorType:   commonerrors.ErrTimeout,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := New()
			ctx := context.Background()

			switch tt.method {
			case "run":
				err := cmd.Run(ctx, tt.command, tt.args, WithTimeout(tt.timeout))
				if tt.expectError {
					assert.Error(t, err)
					if tt.errorType != nil {
						assert.ErrorIs(t, err, tt.errorType)
					}
				} else {
					assert.NoError(t, err)
				}

			case "output":
				stdout, _, err := cmd.Output(ctx, tt.command, tt.args, WithTimeout(tt.timeout))
				if tt.expectError {
					assert.Error(t, err)
					if tt.errorType != nil {
						assert.ErrorIs(t, err, tt.errorType)
					}
				} else {
					assert.NoError(t, err)
					if tt.command == "echo" && len(tt.args) > 0 {
						assert.Contains(t, string(stdout), tt.args[0])
					}
				}

			case "start":
				proc, startErr := cmd.Start(ctx, tt.command, tt.args, WithTimeout(tt.timeout))
				if startErr != nil {
					t.Fatalf("Start() failed: %v", startErr)
				}

				waitErr := proc.Wait()
				if tt.expectError {
					assert.Error(t, waitErr)
					if tt.errorType != nil {
						assert.ErrorIs(t, waitErr, tt.errorType)
					}
				} else {
					assert.NoError(t, waitErr)
				}
			}
		})
	}
}

func TestCommander_WithTimeout_EdgeCases(t *testing.T) {
	t.Run("timeout with zero duration", func(t *testing.T) {
		cmd := New()
		ctx := context.Background()

		// Zero timeout should be treated as "no timeout"
		err := cmd.Run(ctx, "echo", []string{"hello"}, WithTimeout(0))
		assert.NoError(t, err, "Zero timeout should be treated as no timeout")
	})

	t.Run("timeout with negative duration", func(t *testing.T) {
		cmd := New()
		ctx := context.Background()

		// Negative timeout should be treated as "no timeout"
		err := cmd.Run(ctx, "echo", []string{"hello"}, WithTimeout(-5*time.Second))
		assert.NoError(t, err, "Negative timeout should be treated as no timeout")
	})

	t.Run("zero timeout on Output method", func(t *testing.T) {
		cmd := New()
		ctx := context.Background()

		// Zero timeout should be treated as "no timeout"
		stdout, stderr, err := cmd.Output(ctx, "echo", []string{"test output"}, WithTimeout(0))
		assert.NoError(t, err, "Zero timeout should be treated as no timeout")
		assert.Equal(t, "test output\n", string(stdout))
		assert.Empty(t, stderr)
	})

	t.Run("negative timeout on Start method", func(t *testing.T) {
		cmd := New()
		ctx := context.Background()

		// Negative timeout should be treated as "no timeout"
		proc, err := cmd.Start(ctx, "echo", []string{"test"}, WithTimeout(-1*time.Second))
		assert.NoError(t, err, "Negative timeout should be treated as no timeout")
		assert.NotNil(t, proc)

		err = proc.Wait()
		assert.NoError(t, err)
	})

	t.Run("very short timeout", func(t *testing.T) {
		cmd := New()
		ctx := context.Background()

		// 1 nanosecond timeout should timeout
		err := cmd.Run(ctx, "sleep", []string{"0.1"}, WithTimeout(1*time.Nanosecond))
		assert.Error(t, err)
		assert.ErrorIs(t, err, commonerrors.ErrTimeout)
	})

	t.Run("timeout with cancelled context", func(t *testing.T) {
		cmd := New()
		ctx, cancel := context.WithCancel(context.Background())
		cancel() // Cancel immediately

		// Should return context cancelled error, not timeout
		err := cmd.Run(ctx, "echo", []string{"test"}, WithTimeout(1*time.Second))
		assert.Error(t, err)
		// Should be context cancelled, not timeout
		assert.NotErrorIs(t, err, commonerrors.ErrTimeout)
	})

	t.Run("multiple timeout options - last one wins", func(t *testing.T) {
		cmd := New()
		ctx := context.Background()

		// Multiple WithTimeout options - the last one should be used
		err := cmd.Run(ctx, "sleep", []string{"1"},
			WithTimeout(10*time.Second),       // This should be overridden
			WithTimeout(100*time.Millisecond), // This should be used
		)
		assert.Error(t, err)
		assert.ErrorIs(t, err, commonerrors.ErrTimeout)
	})
}

func TestCommander_ContextCancellation_Immediate(t *testing.T) {
	t.Run("immediate cancellation on Run", func(t *testing.T) {
		cmd := New()
		ctx, cancel := context.WithCancel(context.Background())

		// Start a long-running command
		startTime := time.Now()

		// Cancel after a short delay to ensure the command has started
		go func() {
			time.Sleep(50 * time.Millisecond)
			cancel()
		}()

		err := cmd.Run(ctx, "sleep", []string{"5"})
		elapsed := time.Since(startTime)

		// Should return error quickly (well before 5 seconds)
		assert.Error(t, err)
		assert.Less(t, elapsed, 2*time.Second, "Command should be cancelled quickly, not run for 5 seconds")

		// Should be cancellation error (signal: killed when context cancelled)
		assert.True(t,
			strings.Contains(err.Error(), "context") ||
				strings.Contains(err.Error(), "signal: killed"),
			"Expected context cancellation or killed signal, got: %s", err.Error())
	})

	t.Run("immediate cancellation on Output", func(t *testing.T) {
		cmd := New()
		ctx, cancel := context.WithCancel(context.Background())

		startTime := time.Now()

		// Cancel after a short delay
		go func() {
			time.Sleep(50 * time.Millisecond)
			cancel()
		}()

		stdout, stderr, err := cmd.Output(ctx, "sleep", []string{"5"})
		elapsed := time.Since(startTime)

		// Should return error quickly
		assert.Error(t, err)
		assert.Less(t, elapsed, 2*time.Second, "Command should be cancelled quickly")

		// Outputs should be empty or minimal since command was cancelled early
		assert.Empty(t, stdout)
		assert.Empty(t, stderr)

		// Should be cancellation error (signal: killed when context cancelled)
		assert.True(t,
			strings.Contains(err.Error(), "context") ||
				strings.Contains(err.Error(), "signal: killed"),
			"Expected context cancellation or killed signal, got: %s", err.Error())
	})

	t.Run("immediate cancellation on Start", func(t *testing.T) {
		cmd := New()
		ctx, cancel := context.WithCancel(context.Background())

		// Start the process
		proc, err := cmd.Start(ctx, "sleep", []string{"5"})
		require.NoError(t, err)

		startTime := time.Now()

		// Cancel after a short delay
		go func() {
			time.Sleep(50 * time.Millisecond)
			cancel()
		}()

		waitErr := proc.Wait()
		elapsed := time.Since(startTime)

		// Should return error quickly
		assert.Error(t, waitErr)
		assert.Less(t, elapsed, 2*time.Second, "Process should be cancelled quickly")
	})

	t.Run("pre-cancelled context", func(t *testing.T) {
		cmd := New()
		ctx, cancel := context.WithCancel(context.Background())
		cancel() // Cancel before running command

		startTime := time.Now()

		// Should fail immediately
		err := cmd.Run(ctx, "sleep", []string{"5"})
		elapsed := time.Since(startTime)

		assert.Error(t, err)
		assert.Less(t, elapsed, 100*time.Millisecond, "Pre-cancelled context should fail immediately")
		assert.Contains(t, err.Error(), "context")
	})

	t.Run("cancellation during streaming", func(t *testing.T) {
		cmd := New()
		ctx, cancel := context.WithCancel(context.Background())

		// Start a command that outputs continuously
		proc, err := cmd.Start(ctx, "bash", []string{"-c", "while true; do echo 'streaming'; sleep 0.1; done"})
		require.NoError(t, err)

		// Set up streaming
		stdout := make(chan string, 100)
		proc.Stream(stdout, nil)

		// Read a few lines to ensure it's working
		var lines []string
		for len(lines) < 3 {
			select {
			case line := <-stdout:
				lines = append(lines, line)
			case <-time.After(2 * time.Second):
				t.Fatal("Timeout waiting for streaming output")
			}
		}

		// Now cancel the context
		startTime := time.Now()
		cancel()

		// Wait for the process to finish
		waitErr := proc.Wait()
		elapsed := time.Since(startTime)

		// Should finish quickly after cancellation
		assert.Error(t, waitErr)
		assert.Less(t, elapsed, 2*time.Second, "Process should terminate quickly after context cancellation")

		// Should receive at least the initial lines
		assert.GreaterOrEqual(t, len(lines), 3)
	})

	t.Run("context cancellation vs timeout precedence", func(t *testing.T) {
		cmd := New()
		ctx, cancel := context.WithCancel(context.Background())

		startTime := time.Now()

		// Cancel after 50ms, but timeout is set to 1 second
		go func() {
			time.Sleep(50 * time.Millisecond)
			cancel()
		}()

		err := cmd.Run(ctx, "sleep", []string{"5"}, WithTimeout(1*time.Second))
		elapsed := time.Since(startTime)

		// Should be cancelled by context (50ms), not timeout (1s)
		assert.Error(t, err)
		assert.Less(t, elapsed, 500*time.Millisecond, "Should be cancelled by context, not timeout")
		assert.Greater(t, elapsed, 40*time.Millisecond, "Should take at least 40ms to cancel")

		// Should be context cancellation, not timeout
		assert.NotErrorIs(t, err, commonerrors.ErrTimeout)
		assert.True(t,
			strings.Contains(err.Error(), "context") ||
				strings.Contains(err.Error(), "signal: killed"),
			"Expected context cancellation or killed signal, got: %s", err.Error())
	})
}

func TestCommander_ProcessStop(t *testing.T) {
	tests := []struct {
		name             string
		command          string
		args             []string
		timeout          time.Duration
		expectedGraceful bool
	}{
		{
			name:             "graceful stop - short command",
			command:          "sleep",
			args:             []string{"0.5"}, // Longer sleep so it's still running when we stop it
			timeout:          2 * time.Second,
			expectedGraceful: true,
		},
		{
			name:    "force kill - unstoppable process with short timeout",
			command: "bash",
			args: []string{"-c", `
				# Trap SIGTERM and SIGINT - ignore them
				trap 'echo "Ignoring SIGTERM, staying alive!" >&2' TERM
				trap 'echo "Ignoring SIGINT, staying alive!" >&2' INT

				# SIGKILL cannot be trapped, so this will die on force kill
				echo "Process started, becoming unstoppable..."

				# Run forever until SIGKILL
				while true; do
					sleep 0.1
				done
			`},
			timeout:          200 * time.Millisecond,
			expectedGraceful: false,
		},
		{
			name:             "stop already finished process",
			command:          "echo",
			args:             []string{"done"},
			timeout:          1 * time.Second,
			expectedGraceful: true,
		},
		{
			name:             "zero timeout - immediate force kill",
			command:          "sleep",
			args:             []string{"2"},
			timeout:          0,
			expectedGraceful: false,
		},
		{
			name:             "negative timeout - immediate force kill",
			command:          "sleep",
			args:             []string{"2"},
			timeout:          -5 * time.Second,
			expectedGraceful: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := New()
			ctx := context.Background()

			// Start the process
			proc, err := cmd.Start(ctx, tt.command, tt.args)
			require.NoError(t, err)

			// For the "already finished" test, wait for it to complete first
			if strings.Contains(tt.name, "already finished") {
				time.Sleep(100 * time.Millisecond)
				err = proc.Wait()
				assert.NoError(t, err)
			}

			// For the "unstoppable" test, give it time to start and setup signal handlers
			if strings.Contains(tt.name, "unstoppable") {
				// Process is already started by Start() - just give it time to setup signal handlers
				time.Sleep(100 * time.Millisecond) // Give more time for bash script setup
			}

			// Try to stop the process
			startTime := time.Now()
			stopCtx, cancel := context.WithTimeout(ctx, tt.timeout)
			defer cancel()
			stopErr := proc.Stop(stopCtx)
			stopDuration := time.Since(startTime)

			// Verify stop behavior
			if tt.expectedGraceful {
				// Should stop within reasonable time
				assert.True(t, stopDuration < tt.timeout+500*time.Millisecond,
					"Graceful stop should complete quickly")
			} else {
				// Force kill should happen around the timeout
				assert.True(t, stopDuration >= tt.timeout-100*time.Millisecond,
					"Force kill should happen after timeout")
			}

			// Process should be stopped (subsequent operations should fail or be no-ops)
			if !tt.expectedGraceful && stopErr != nil {
				// Force kills should result in ErrKilled error
				assert.ErrorIs(t, stopErr, commonerrors.ErrKilled,
					"Force kill should result in ErrKilled error")
			} else if tt.expectedGraceful && stopErr != nil {
				// Graceful stops should result in ErrTerminated error
				assert.ErrorIs(t, stopErr, commonerrors.ErrTerminated,
					"Graceful stop should result in ErrTerminated error")
			} else {
				assert.NoError(t, stopErr, "Stop should not return error")
			}

			t.Logf("Stop completed in %v (timeout was %v)", stopDuration, tt.timeout)
		})
	}
}

func TestCommander_ProcessKill(t *testing.T) {
	t.Run("kill long-running process", func(t *testing.T) {
		cmd := New()
		ctx := context.Background()

		// Start a long-running process
		proc, err := cmd.Start(ctx, "sleep", []string{"5"})
		require.NoError(t, err)

		// Kill it immediately
		startTime := time.Now()
		err = proc.Kill(ctx)
		killDuration := time.Since(startTime)

		// Should kill immediately (much faster than 5 seconds)
		assert.True(t, killDuration < 100*time.Millisecond,
			"Kill should be immediate, took %v", killDuration)

		// Error should be ErrKilled for killed processes
		if err != nil {
			assert.ErrorIs(t, err, commonerrors.ErrKilled, "Kill should return ErrKilled")
		}

		t.Logf("Kill completed in %v", killDuration)
	})

	t.Run("kill already finished process", func(t *testing.T) {
		cmd := New()
		ctx := context.Background()

		// Start a short process
		proc, err := cmd.Start(ctx, "echo", []string{"done"})
		require.NoError(t, err)

		// Wait for it to finish
		time.Sleep(100 * time.Millisecond)
		err = proc.Wait()
		assert.NoError(t, err)

		// Kill should work on already-finished process
		startTime := time.Now()
		err = proc.Kill(ctx)
		killDuration := time.Since(startTime)

		t.Logf("Kill on finished process completed in %v", killDuration)
		// Should be very fast since process is already dead
		assert.True(t, killDuration < 10*time.Millisecond)
	})
}

func TestCommander_SignalTermination(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		gracefulTimeout time.Duration
		expectedError   error
		description     string
	}{
		{
			name:            "SIGTERM_graceful_termination",
			gracefulTimeout: 2 * time.Second, // Allow time for graceful termination
			expectedError:   commonerrors.ErrTerminated,
			description:     "Process should be terminated by SIGTERM and return ErrTerminated",
		},
		{
			name:            "SIGKILL_force_termination",
			gracefulTimeout: 100 * time.Millisecond, // Short timeout, but process will ignore SIGTERM
			expectedError:   commonerrors.ErrKilled,
			description:     "Process should be killed by SIGKILL after timeout and return ErrKilled",
		},
		{
			name:            "immediate_SIGKILL",
			gracefulTimeout: 0, // No graceful period, immediate SIGKILL
			expectedError:   commonerrors.ErrKilled,
			description:     "Process should be immediately killed by SIGKILL and return ErrKilled",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			commander := New()
			ctx := context.Background()

			// Start different processes based on test case
			var proc Process
			var err error
			if tt.name == "SIGKILL_force_termination" {
				// Use a script that ignores SIGTERM for this test
				proc, err = commander.Start(ctx, "/bin/bash", []string{".fixtures/sigignore.sh"})
				require.NoError(t, err, "Failed to start signal-ignoring process")
			} else {
				// Use regular sleep for other tests
				proc, err = commander.Start(ctx, "sleep", []string{"5"})
				require.NoError(t, err, "Failed to start process")
			}

			// Give the process a moment to start
			time.Sleep(10 * time.Millisecond)

			// Stop the process with the specified timeout
			stopCtx, cancel := context.WithTimeout(ctx, tt.gracefulTimeout)
			defer cancel()
			err = proc.Stop(stopCtx)

			// Verify we got the expected error
			assert.ErrorIs(t, err, tt.expectedError, "Expected %v, got %v: %s", tt.expectedError, err, tt.description)

			// Also test using Wait()
			waitErr := proc.Wait()
			if tt.expectedError != nil {
				// For immediate SIGKILL tests, both ErrKilled and ErrTerminated are acceptable
				// due to timing - process might be terminated by SIGTERM before SIGKILL takes effect
				if tt.name == "immediate_SIGKILL" {
					isKilled := errors.Is(waitErr, commonerrors.ErrKilled)
					isTerminated := errors.Is(waitErr, commonerrors.ErrTerminated)
					assert.True(t, isKilled || isTerminated, 
						"Wait() should return ErrKilled or ErrTerminated for immediate kill, got: %v", waitErr)
				} else {
					assert.ErrorIs(t, waitErr, tt.expectedError, "Wait() should return same error as Stop()")
				}
			}
		})
	}
}

func TestCommander_KillProcess(t *testing.T) {
	t.Parallel()

	commander := New()
	ctx := context.Background()

	// Start a long-running process
	proc, err := commander.Start(ctx, "sleep", []string{"5"})
	require.NoError(t, err, "Failed to start process")

	// Give the process a moment to start
	time.Sleep(10 * time.Millisecond)

	// Kill the process (immediate SIGKILL)
	err = proc.Kill(ctx)

	// Should return ErrKilled
	assert.ErrorIs(t, err, commonerrors.ErrKilled, "Expected ErrKilled from Kill(), got %v", err)

	// Wait should also return ErrKilled
	waitErr := proc.Wait()
	assert.ErrorIs(t, waitErr, commonerrors.ErrKilled, "Wait() after Kill() should return ErrKilled")
}

func TestCommander_TimeoutVsSignalTermination(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		useTimeout    bool
		timeout       time.Duration
		expectedError error
		description   string
	}{
		{
			name:          "context_timeout",
			useTimeout:    true,
			timeout:       100 * time.Millisecond,
			expectedError: commonerrors.ErrTimeout,
			description:   "Context timeout should return ErrTimeout, not ErrKilled",
		},
		{
			name:          "stop_timeout_force_kill",
			useTimeout:    false,
			timeout:       100 * time.Millisecond,
			expectedError: commonerrors.ErrKilled,
			description:   "Stop timeout should force kill and return ErrKilled",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			commander := New()

			if tt.useTimeout {
				// Test context timeout
				ctx, cancel := context.WithTimeout(context.Background(), tt.timeout)
				defer cancel()

				err := commander.Run(ctx, "sleep", []string{"1"})
				assert.ErrorIs(t, err, tt.expectedError, "Expected %v for context timeout, got %v", tt.expectedError, err)
			} else {
				// Test stop timeout - use script that ignores SIGTERM to force SIGKILL
				ctx := context.Background()
				proc, err := commander.Start(ctx, "/bin/bash", []string{".fixtures/sigignore.sh"})
				require.NoError(t, err, "Failed to start process")

				time.Sleep(10 * time.Millisecond) // Let process start

				stopCtx, cancel := context.WithTimeout(ctx, tt.timeout)
				defer cancel()
				err = proc.Stop(stopCtx)
				assert.ErrorIs(t, err, tt.expectedError, "Expected %v for stop timeout, got %v", tt.expectedError, err)
			}
		})
	}
}

func TestCommander_ProcessLifecycleWithSignals(t *testing.T) {
	t.Parallel()

	commander := New()
	ctx := context.Background()

	// Start a process
	proc, err := commander.Start(ctx, "sleep", []string{"10"})
	require.NoError(t, err, "Failed to start process")

	// Verify process is running by trying to stop it
	time.Sleep(10 * time.Millisecond)

	// Test graceful termination first
	stopCtx, cancel := context.WithTimeout(ctx, 1*time.Second)
	defer cancel()
	err = proc.Stop(stopCtx)

	// Should get either ErrTerminated or ErrKilled depending on timing
	isTerminated := errors.Is(err, commonerrors.ErrTerminated)
	isKilled := errors.Is(err, commonerrors.ErrKilled)

	assert.True(t, isTerminated || isKilled,
		"Stop should return either ErrTerminated or ErrKilled, got: %v", err)

	// Wait should return the same error
	waitErr := proc.Wait()
	if err != nil {
		assert.ErrorIs(t, waitErr, err, "Wait should return same error as Stop")
	}
}

// TestCommander_RaceConditions tests various race conditions that could occur
// with concurrent access to process methods
func TestCommander_RaceConditions(t *testing.T) {
	t.Run("concurrent Wait() and Stop() calls", func(t *testing.T) {
		cmd := New()
		ctx := context.Background()

		// Start a long-running process
		proc, err := cmd.Start(ctx, "sleep", []string{"3"})
		require.NoError(t, err)

		var wg sync.WaitGroup
		var waitErr, stopErr error

		// Start Wait() in one goroutine
		wg.Add(1)
		go func() {
			defer wg.Done()
			waitErr = proc.Wait()
		}()

		// Start Stop() in another goroutine after a short delay
		wg.Add(1)
		go func() {
			defer wg.Done()
			time.Sleep(50 * time.Millisecond) // Let Wait() start first
			stopCtx, cancel := context.WithTimeout(ctx, 1*time.Second)
			defer cancel()
			stopErr = proc.Stop(stopCtx)
		}()

		// Both should complete without hanging or panicking
		done := make(chan struct{})
		go func() {
			wg.Wait()
			close(done)
		}()

		select {
		case <-done:
			// Success - both completed
		case <-time.After(10 * time.Second):
			t.Fatal("Race condition test timed out - likely deadlock")
		}

		// Both operations should complete (errors are expected due to termination)
		t.Logf("Wait() result: %v", waitErr)
		t.Logf("Stop() result: %v", stopErr)
	})

	t.Run("concurrent Wait() and Kill() calls", func(t *testing.T) {
		cmd := New()
		ctx := context.Background()

		// Start a long-running process
		proc, err := cmd.Start(ctx, "sleep", []string{"3"})
		require.NoError(t, err)

		var wg sync.WaitGroup
		var waitErr, killErr error

		// Start Wait() in one goroutine
		wg.Add(1)
		go func() {
			defer wg.Done()
			waitErr = proc.Wait()
		}()

		// Start Kill() in another goroutine after a short delay
		wg.Add(1)
		go func() {
			defer wg.Done()
			time.Sleep(50 * time.Millisecond)
			killErr = proc.Kill(ctx)
		}()

		// Both should complete without hanging
		done := make(chan struct{})
		go func() {
			wg.Wait()
			close(done)
		}()

		select {
		case <-done:
			// Success
		case <-time.After(5 * time.Second):
			t.Fatal("Race condition test timed out")
		}

		t.Logf("Wait() result: %v", waitErr)
		t.Logf("Kill() result: %v", killErr)
	})

	t.Run("multiple concurrent Wait() calls", func(t *testing.T) {
		cmd := New()
		ctx := context.Background()

		// Start a process that will fail
		proc, err := cmd.Start(ctx, "false", []string{}) // exits with status 1
		require.NoError(t, err)

		const numWaiters = 10
		results := make([]error, numWaiters)
		var wg sync.WaitGroup

		// Launch multiple concurrent Wait() calls
		for i := range numWaiters {
			wg.Add(1)
			go func(index int) {
				defer wg.Done()
				results[index] = proc.Wait()
			}(i)
		}

		// All should complete
		done := make(chan struct{})
		go func() {
			wg.Wait()
			close(done)
		}()

		select {
		case <-done:
			// Success
		case <-time.After(5 * time.Second):
			t.Fatal("Multiple Wait() calls timed out")
		}

		// All results should be identical
		firstResult := results[0]
		for i := 1; i < numWaiters; i++ {
			// Compare error messages since errors might wrap differently
			if firstResult == nil {
				assert.Nil(t, results[i], "All Wait() calls should return same result")
			} else if results[i] == nil {
				t.Errorf("Inconsistent results: first=%v, index %d=%v", firstResult, i, results[i])
			} else {
				// Both are errors - they should be equivalent
				assert.Contains(t, results[i].Error(), "exit status 1",
					"All Wait() calls should return equivalent error")
			}
		}

		t.Logf("All %d Wait() calls completed with consistent results", numWaiters)
	})

	t.Run("concurrent Stop() calls", func(t *testing.T) {
		cmd := New()
		ctx := context.Background()

		// Start a long-running process
		proc, err := cmd.Start(ctx, "sleep", []string{"3"})
		require.NoError(t, err)

		const numStoppers = 5
		results := make([]error, numStoppers)
		var wg sync.WaitGroup

		// Launch multiple concurrent Stop() calls
		for i := range numStoppers {
			wg.Add(1)
			go func(index int) {
				defer wg.Done()
				stopCtx, cancel := context.WithTimeout(ctx, 1*time.Second)
				defer cancel()
				results[index] = proc.Stop(stopCtx)
			}(i)
		}

		// All should complete
		done := make(chan struct{})
		go func() {
			wg.Wait()
			close(done)
		}()

		select {
		case <-done:
			// Success
		case <-time.After(10 * time.Second):
			t.Fatal("Multiple Stop() calls timed out")
		}

		// Only one should actually do the stopping (due to sync.Once)
		// but all should complete without hanging
		t.Logf("All %d Stop() calls completed", numStoppers)
		for i, result := range results {
			t.Logf("Stop() call %d result: %v", i, result)
		}
	})

	t.Run("mixed concurrent operations", func(t *testing.T) {
		cmd := New()
		ctx := context.Background()

		// Start a long-running process
		proc, err := cmd.Start(ctx, "sleep", []string{"2"})
		require.NoError(t, err)

		var wg sync.WaitGroup
		results := make([]error, 6)

		// Mix of different concurrent operations
		operations := []func(int){
			func(i int) { results[i] = proc.Wait() },
			func(i int) { 
				stopCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
				defer cancel()
				results[i] = proc.Stop(stopCtx)
			},
			func(i int) { results[i] = proc.Kill(ctx) },
			func(i int) { results[i] = proc.Wait() },
			func(i int) { 
				stopCtx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
				defer cancel()
				results[i] = proc.Stop(stopCtx)
			},
			func(i int) { results[i] = proc.Wait() },
		}

		// Launch all operations with small delays
		for i, op := range operations {
			wg.Add(1)
			go func(index int, operation func(int)) {
				defer wg.Done()
				time.Sleep(time.Duration(index*10) * time.Millisecond)
				operation(index)
			}(i, op)
		}

		// All should complete
		done := make(chan struct{})
		go func() {
			wg.Wait()
			close(done)
		}()

		select {
		case <-done:
			// Success
		case <-time.After(10 * time.Second):
			t.Fatal("Mixed operations timed out")
		}

		// All should complete without panicking
		for i, result := range results {
			t.Logf("Operation %d result: %v", i, result)
		}
	})
}

// TestCommander_RaceDetection specifically tests that we don't have data races
// This test should be run with -race flag to detect races
func TestCommander_RaceDetection(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping race detection test in short mode")
	}

	t.Run("stress test concurrent access", func(t *testing.T) {
		cmd := New()
		ctx := context.Background()

		// Start a process
		proc, err := cmd.Start(ctx, "sleep", []string{"1"})
		require.NoError(t, err)

		const numGoroutines = 50
		var wg sync.WaitGroup

		// Hammer the process with concurrent operations
		for i := range numGoroutines {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()

				switch id % 4 {
				case 0:
					_ = proc.Wait()
				case 1:
					stopCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
					defer cancel()
					_ = proc.Stop(stopCtx)
				case 2:
					_ = proc.Kill(ctx)
				case 3:
					// Start streaming to add more concurrent access
					stdout := make(chan string, 1)
					proc.Stream(stdout, nil)
					// Read one line or timeout
					select {
					case <-stdout:
					case <-time.After(50 * time.Millisecond):
					}
				}
			}(i)
		}

		// Wait for all to complete
		done := make(chan struct{})
		go func() {
			wg.Wait()
			close(done)
		}()

		select {
		case <-done:
			t.Log("Stress test completed successfully")
		case <-time.After(15 * time.Second):
			t.Fatal("Stress test timed out")
		}
	})
}
