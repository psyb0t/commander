package commander

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMockCommander_Basic(t *testing.T) {
	tests := []struct {
		name        string
		setupMock   func(*MockCommander)
		testFunc    func(*testing.T, Commander)
		expectError bool
	}{
		{
			name: "simple expectation",
			setupMock: func(m *MockCommander) {
				m.Expect("echo", "test").ReturnOutput([]byte("mocked output"))
			},
			testFunc: func(t *testing.T, cmd Commander) {
				stdout, stderr, err := cmd.Output(context.Background(), "echo", []string{"test"})
				require.NoError(t, err)
				assert.Equal(t, "mocked output", string(stdout))
				assert.Equal(t, "mocked output", string(stderr))
			},
		},
		{
			name: "expectation with error",
			setupMock: func(m *MockCommander) {
				m.Expect("failing", "cmd").ReturnError(errors.New("mock error"))
			},
			testFunc: func(t *testing.T, cmd Commander) {
				err := cmd.Run(context.Background(), "failing", []string{"cmd"})
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "mock error")
			},
		},
		{
			name: "unexpected command",
			setupMock: func(m *MockCommander) {
				m.Expect("expected", "cmd")
			},
			testFunc: func(t *testing.T, cmd Commander) {
				err := cmd.Run(context.Background(), "unexpected", []string{"cmd"})
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "unexpected command")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := NewMock()
			tt.setupMock(mock)
			tt.testFunc(t, mock)
		})
	}
}

func TestMockCommander_ArgumentMatchers(t *testing.T) {
	tests := []struct {
		name        string
		matchers    []ArgumentMatcher
		testArgs    []string
		shouldMatch bool
	}{
		{
			name:        "exact match",
			matchers:    []ArgumentMatcher{Exact("test"), Exact("arg")},
			testArgs:    []string{"test", "arg"},
			shouldMatch: true,
		},
		{
			name:        "exact mismatch",
			matchers:    []ArgumentMatcher{Exact("test"), Exact("arg")},
			testArgs:    []string{"test", "different"},
			shouldMatch: false,
		},
		{
			name:        "regex match",
			matchers:    []ArgumentMatcher{Regex("^test.*"), Exact("arg")},
			testArgs:    []string{"test123", "arg"},
			shouldMatch: true,
		},
		{
			name:        "regex mismatch",
			matchers:    []ArgumentMatcher{Regex("^test.*"), Exact("arg")},
			testArgs:    []string{"nottest", "arg"},
			shouldMatch: false,
		},
		{
			name:        "any matcher",
			matchers:    []ArgumentMatcher{Any(), Any()},
			testArgs:    []string{"anything", "goes"},
			shouldMatch: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := NewMock()
			mock.ExpectWithMatchers("cmd", tt.matchers...).ReturnOutput([]byte("matched"))

			stdout, stderr, err := mock.Output(context.Background(), "cmd", tt.testArgs)

			if tt.shouldMatch {
				require.NoError(t, err)
				assert.Equal(t, "matched", string(stdout))
				assert.Equal(t, "matched", string(stderr))
			} else {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "unexpected command")
			}
		})
	}
}

func TestMockCommander_ProcessControl(t *testing.T) {
	t.Run("start and wait", func(t *testing.T) {
		mock := NewMock()
		mock.Expect("echo", "test").ReturnOutput([]byte("process output"))

		proc, err := mock.Start(context.Background(), "echo", []string{"test"})
		require.NoError(t, err)

		// Use Stream() instead of StdoutPipe
		stdout := make(chan string, 10)
		proc.Stream(stdout, nil)

		// Collect all output
		var lines []string
		for line := range stdout {
			lines = append(lines, line)
		}

		// Verify we got the expected output
		require.Len(t, lines, 1)
		assert.Equal(t, "process output", lines[0])

		err = proc.Wait()
		require.NoError(t, err)
	})

	t.Run("stop process", func(t *testing.T) {
		mock := NewMock()
		mock.Expect("sleep", "10").ReturnOutput([]byte("sleeping"))

		proc, err := mock.Start(context.Background(), "sleep", []string{"10"})
		require.NoError(t, err)

		// Stop should work without error
		err = proc.Stop(context.Background(), 1*time.Second)
		assert.NoError(t, err)

		err = proc.Wait()
		require.NoError(t, err)
	})

	t.Run("combined output", func(t *testing.T) {
		mock := NewMock()
		mock.Expect("sh", "-c", "echo combined").ReturnOutput([]byte("stdout and stderr"))

		stdout, stderr, err := mock.Output(context.Background(), "sh", []string{"-c", "echo combined"})
		require.NoError(t, err)
		assert.Equal(t, "stdout and stderr", string(stdout))
		assert.Equal(t, "stdout and stderr", string(stderr))

		err = mock.VerifyExpectations()
		require.NoError(t, err)
	})
}

func TestMockCommander_Verification(t *testing.T) {
	tests := []struct {
		name            string
		setupFunc       func(*MockCommander)
		executeFunc     func(*testing.T, *MockCommander)
		expectVerifyErr bool
		verifyFunc      func(*testing.T, *MockCommander)
	}{
		{
			name: "all expectations met",
			setupFunc: func(mock *MockCommander) {
				mock.Expect("cmd1").ReturnOutput([]byte("out1"))
				mock.Expect("cmd2").ReturnOutput([]byte("out2"))
			},
			executeFunc: func(t *testing.T, mock *MockCommander) {
				_, _, err := mock.Output(context.Background(), "cmd1", []string{})
				require.NoError(t, err)
				_, _, err = mock.Output(context.Background(), "cmd2", []string{})
				require.NoError(t, err)
			},
			expectVerifyErr: false,
		},
		{
			name: "unmet expectation",
			setupFunc: func(mock *MockCommander) {
				mock.Expect("cmd1").ReturnOutput([]byte("out1"))
				mock.Expect("cmd2").ReturnOutput([]byte("out2"))
			},
			executeFunc: func(t *testing.T, mock *MockCommander) {
				_, _, err := mock.Output(context.Background(), "cmd1", []string{})
				require.NoError(t, err)
				// Don't call cmd2 - leave expectation unmet
			},
			expectVerifyErr: true,
			verifyFunc: func(t *testing.T, mock *MockCommander) {
				err := mock.VerifyExpectations()
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "expected command not called")
			},
		},
		{
			name: "call order tracking",
			setupFunc: func(mock *MockCommander) {
				mock.Expect("first").ReturnOutput([]byte("1"))
				mock.Expect("second").ReturnOutput([]byte("2"))
			},
			executeFunc: func(t *testing.T, mock *MockCommander) {
				_, _, err := mock.Output(context.Background(), "first", []string{})
				require.NoError(t, err)
				_, _, err = mock.Output(context.Background(), "second", []string{})
				require.NoError(t, err)
			},
			expectVerifyErr: false,
			verifyFunc: func(t *testing.T, mock *MockCommander) {
				order := mock.CallOrder()
				assert.Equal(t, []string{"first ", "second "}, order)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := NewMock()

			if tt.setupFunc != nil {
				tt.setupFunc(mock)
			}

			if tt.executeFunc != nil {
				tt.executeFunc(t, mock)
			}

			if tt.verifyFunc != nil {
				tt.verifyFunc(t, mock)
			} else {
				err := mock.VerifyExpectations()
				if tt.expectVerifyErr {
					assert.Error(t, err)
				} else {
					assert.NoError(t, err)
				}
			}
		})
	}
}

func TestMockCommander_Reset(t *testing.T) {
	mock := NewMock()
	mock.Expect("cmd1").ReturnOutput([]byte("out1"))

	// Reset should clear expectations
	mock.Reset()

	err := mock.VerifyExpectations()
	assert.NoError(t, err) // No expectations to verify

	// Command should now be unexpected
	err = mock.Run(context.Background(), "cmd1", []string{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unexpected command")
}

func TestConcurrentMockAccess(t *testing.T) {
	mock := NewMock()

	// Add expectations for concurrent access
	for i := range 10 {
		mock.Expect("echo", string(rune('a'+i))).ReturnOutput([]byte(string(rune('A' + i))))
	}

	// Run commands concurrently
	results := make(chan string, 10)
	errors := make(chan error, 10)

	for i := range 10 {
		go func(index int) {
			stdout, _, err := mock.Output(context.Background(), "echo", []string{string(rune('a' + index))})
			if err != nil {
				errors <- err
			} else {
				results <- string(stdout)
			}
		}(i)
	}

	// Collect results
	var outputs []string
	for range 10 {
		select {
		case err := <-errors:
			t.Fatalf("Unexpected error: %v", err)
		case output := <-results:
			outputs = append(outputs, output)
		case <-time.After(2 * time.Second):
			t.Fatal("Timeout waiting for results")
		}
	}

	assert.Len(t, outputs, 10)

	err := mock.VerifyExpectations()
	assert.NoError(t, err)
}

func TestArgumentMatchers_String(t *testing.T) {
	tests := []struct {
		name     string
		matcher  ArgumentMatcher
		expected string
	}{
		{
			name:     "exact matcher",
			matcher:  Exact("test"),
			expected: "test",
		},
		{
			name:     "regex matcher",
			matcher:  Regex("^test.*"),
			expected: "regex:^test.*",
		},
		{
			name:     "any matcher",
			matcher:  Any(),
			expected: "*",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.matcher.String())
		})
	}
}

func TestMockCommander_Stream(t *testing.T) {
	tests := []struct {
		name     string
		command  string
		args     []string
		output   []byte
		testFunc func(t *testing.T, proc Process)
	}{
		{
			name:    "basic mock streaming",
			command: "ping",
			args:    []string{"google.com"},
			output:  []byte("line1\nline2\nline3"),
			testFunc: func(t *testing.T, proc Process) {
				// Start streaming
				ch := make(chan string, 10)
				proc.Stream(ch, nil)

				// Collect streamed lines
				var lines []string
				for line := range ch {
					lines = append(lines, line)
				}

				// Should get the lines we configured
				assert.Equal(t, []string{"line1", "line2", "line3"}, lines)
			},
		},
		{
			name:    "mock multiple lines streaming",
			command: "echo",
			args:    []string{"test"},
			output:  []byte("line1\nline2\nline3\nline4\nline5"),
			testFunc: func(t *testing.T, proc Process) {
				// Start multiple streams
				ch1 := make(chan string, 10)
				ch2 := make(chan string, 10)
				ch3 := make(chan string, 10)

				proc.Stream(ch1, nil)
				proc.Stream(ch2, nil)
				proc.Stream(ch3, nil)

				// Collect from all streams
				var lines1, lines2, lines3 []string

				for line := range ch1 {
					lines1 = append(lines1, line)
				}
				for line := range ch2 {
					lines2 = append(lines2, line)
				}
				for line := range ch3 {
					lines3 = append(lines3, line)
				}

				// All streams should get the same lines (broadcast behavior)
				expectedLines := []string{"line1", "line2", "line3", "line4", "line5"}

				// All streams should get all lines since they're started simultaneously
				assert.Equal(t, expectedLines, lines1, "Stream 1 should get all lines")
				assert.Equal(t, expectedLines, lines2, "Stream 2 should get all lines")
				assert.Equal(t, expectedLines, lines3, "Stream 3 should get all lines")
			},
		},
		{
			name:    "mock empty output",
			command: "true",
			args:    []string{},
			output:  []byte(""),
			testFunc: func(t *testing.T, proc Process) {
				ch := make(chan string, 10)
				proc.Stream(ch, nil)

				var lines []string
				for line := range ch {
					lines = append(lines, line)
				}

				assert.Empty(t, lines, "Should have no lines for empty output")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := NewMock()
			mock.Expect(tt.command, tt.args...).ReturnOutput(tt.output)

			proc, err := mock.Start(context.Background(), tt.command, tt.args)
			require.NoError(t, err)

			tt.testFunc(t, proc)
		})
	}
}

func TestMockCommander_StreamAdvanced(t *testing.T) {
	t.Run("mock live streaming position tracking", func(t *testing.T) {
		mock := NewMock()
		mock.Expect("command").ReturnOutput([]byte("line1\nline2"))

		proc, err := mock.Start(context.Background(), "command", []string{})
		require.NoError(t, err)

		// Cast to mock process for advanced control
		mockProc := proc.(*mockProcess)

		// First stream gets all available lines
		ch1 := make(chan string, 10)
		proc.Stream(ch1, nil)

		lines1 := make([]string, 0)
		for line := range ch1 {
			lines1 = append(lines1, line)
		}
		assert.Equal(t, []string{"line1", "line2"}, lines1)

		// Manually advance position to simulate "live" behavior
		mockProc.streamMu.Lock()
		mockProc.streamIndex = len(mockProc.streamLines)
		mockProc.streamMu.Unlock()

		// Add new lines
		mockProc.SimulateStreamLine("line3")
		mockProc.SimulateStreamLine("line4")

		// Second stream should only get the new lines
		ch2 := make(chan string, 10)
		proc.Stream(ch2, nil)

		lines2 := make([]string, 0)
		for line := range ch2 {
			lines2 = append(lines2, line)
		}
		assert.Equal(t, []string{"line3", "line4"}, lines2)
	})
}
