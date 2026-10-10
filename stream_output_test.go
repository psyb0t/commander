package commander

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	fastCommandIterations = 2000
	streamTestBuffer      = 64
	drainReadTimeout      = 10 * time.Second
	seqLineCount          = 1000
	// drainBoundSlack is the allowance on top of outputDrainTimeout for
	// scheduling and the deferred Stop.
	drainBoundSlack = 2 * time.Second
)

// collectLines reads ch until it is closed, failing the test if it does not
// close within drainReadTimeout.
func collectLines(t *testing.T, ch <-chan string) []string {
	t.Helper()

	var lines []string

	timeout := time.After(drainReadTimeout)

	for {
		select {
		case line, ok := <-ch:
			if !ok {
				return lines
			}

			lines = append(lines, line)
		case <-timeout:
			require.FailNow(t, "stream channel was not closed in time", "got %q", lines)

			return lines
		}
	}
}

func TestStream_FastCommandConcurrentWait(t *testing.T) {
	for i := range fastCommandIterations {
		out := make(chan string, streamTestBuffer)

		proc, err := New().Start(
			context.Background(),
			"sh",
			[]string{"-c", "echo hello"},
			WithStream(out, nil),
		)
		require.NoError(t, err)

		waitDone := make(chan error, 1)

		go func() { waitDone <- proc.Wait() }()

		got := collectLines(t, out)
		require.Equal(t, []string{"hello"}, got, "iteration %d", i)
		require.NoError(t, <-waitDone)
	}
}

func TestStream_FastCommandWaitFirst(t *testing.T) {
	for i := range fastCommandIterations {
		out := make(chan string, streamTestBuffer)

		proc, err := New().Start(
			context.Background(),
			"sh",
			[]string{"-c", "echo hello"},
			WithStream(out, nil),
		)
		require.NoError(t, err)
		require.NoError(t, proc.Wait())

		got := collectLines(t, out)
		require.Equal(t, []string{"hello"}, got, "iteration %d", i)
	}
}

func TestStream_FastCommandStderr(t *testing.T) {
	for i := range fastCommandIterations {
		out := make(chan string, streamTestBuffer)
		errCh := make(chan string, streamTestBuffer)

		proc, err := New().Start(
			context.Background(),
			"sh",
			[]string{"-c", "echo out; echo err >&2"},
			WithStream(out, errCh),
		)
		require.NoError(t, err)
		require.NoError(t, proc.Wait())

		require.Equal(t, []string{"out"}, collectLines(t, out), "iteration %d", i)
		require.Equal(t, []string{"err"}, collectLines(t, errCh), "iteration %d", i)
	}
}

func TestStream_SeqDeliversAllLinesInOrder(t *testing.T) {
	out := make(chan string, streamTestBuffer)

	proc, err := New().Start(
		context.Background(),
		"seq",
		[]string{"1", strconv.Itoa(seqLineCount)},
		WithStream(out, nil),
	)
	require.NoError(t, err)

	waitDone := make(chan error, 1)

	go func() { waitDone <- proc.Wait() }()

	got := collectLines(t, out)
	require.Len(t, got, seqLineCount)

	for i, line := range got {
		require.Equal(t, strconv.Itoa(i+1), line)
	}

	require.NoError(t, <-waitDone)
}

func TestStream_GrandchildHoldingPipeDoesNotHangWait(t *testing.T) {
	out := make(chan string, streamTestBuffer)

	proc, err := New().Start(
		context.Background(),
		"sh",
		[]string{"-c", "sleep 5 & echo hi"},
		WithStream(out, nil),
	)
	require.NoError(t, err)

	start := time.Now()

	waitDone := make(chan error, 1)

	go func() { waitDone <- proc.Wait() }()

	select {
	case err := <-waitDone:
		require.NoError(t, err)
	case <-time.After(outputDrainTimeout + drainBoundSlack):
		require.FailNow(t, "Wait hung on a grandchild holding the output pipe")
	}

	assert.Less(t, time.Since(start), outputDrainTimeout+drainBoundSlack)
	assert.Equal(t, []string{"hi"}, collectLines(t, out))
}

func TestStream_StreamAfterStartStillWorks(t *testing.T) {
	proc, err := New().Start(
		context.Background(),
		"sh",
		[]string{"-c", "sleep 0.2; echo late"},
	)
	require.NoError(t, err)

	out := make(chan string, streamTestBuffer)
	proc.Stream(out, nil)

	require.NoError(t, proc.Wait())
	assert.Equal(t, []string{"late"}, collectLines(t, out))
}
