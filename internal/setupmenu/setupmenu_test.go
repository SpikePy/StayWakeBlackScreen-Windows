package setupmenu

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

// silentInput returns Lines that deliver nothing until the test ends,
// like a console window nobody is typing into.
func silentInput(t *testing.T) Lines {
	t.Helper()
	pr, pw := io.Pipe()
	t.Cleanup(func() { pw.Close() })
	return ReadLines(pr)
}

type promptResult struct {
	action string
	auto   bool
	err    error
}

func TestPromptManualChoice(t *testing.T) {
	tests := map[string]string{
		"1\n":     "install",
		"2\n":     "uninstall",
		" 2 \r\n": "uninstall", // Windows consoles end lines with \r\n
	}
	for input, want := range tests {
		action, auto, err := Prompt(ReadLines(strings.NewReader(input)), io.Discard, time.Hour)
		if err != nil || action != want || auto {
			t.Errorf("input %q: got (%q, auto=%v, %v), want (%q, auto=false, nil)", input, action, auto, err, want)
		}
	}
}

func TestPromptTimesOutToInstall(t *testing.T) {
	var out bytes.Buffer
	action, auto, err := Prompt(silentInput(t), &out, 20*time.Millisecond)
	if err != nil || action != "install" || !auto {
		t.Fatalf("got (%q, auto=%v, %v), want (install, auto=true, nil)", action, auto, err)
	}
	if !strings.Contains(out.String(), "No input received") {
		t.Errorf("output is missing the auto-install notice:\n%s", out.String())
	}
}

func TestPromptTypingCancelsCountdown(t *testing.T) {
	pr, pw := io.Pipe()
	defer pw.Close()
	go io.WriteString(pw, "9\n") // invalid, but proves someone is there
	in := ReadLines(pr)

	var out bytes.Buffer
	done := make(chan promptResult, 1)
	go func() {
		action, auto, err := Prompt(in, &out, 100*time.Millisecond)
		done <- promptResult{action, auto, err}
	}()

	select {
	case r := <-done:
		t.Fatalf("Prompt returned %+v after an invalid choice; want it to keep waiting", r)
	case <-time.After(400 * time.Millisecond): // well past the countdown
	}

	io.WriteString(pw, "2\n")
	r := <-done
	if r.err != nil || r.action != "uninstall" || r.auto {
		t.Fatalf("got %+v, want uninstall, auto=false", r)
	}
	if got := strings.Count(out.String(), "automatically in"); got != 1 {
		t.Errorf("countdown shown %d times, want only on the first prompt", got)
	}
}

func TestPromptInputError(t *testing.T) {
	_, _, err := Prompt(ReadLines(strings.NewReader("")), io.Discard, time.Hour)
	if !errors.Is(err, io.EOF) {
		t.Fatalf("got %v, want an error wrapping io.EOF", err)
	}
}

func TestWaitForEnterExitsOnItsOwn(t *testing.T) {
	in := silentInput(t)
	var out bytes.Buffer
	done := make(chan struct{})
	go func() {
		WaitForEnter(in, &out, 20*time.Millisecond)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("WaitForEnter didn't exit on its own")
	}
	if !strings.Contains(out.String(), "Exiting automatically") {
		t.Errorf("output is missing the auto-exit notice:\n%s", out.String())
	}
}

func TestWaitForEnterWithoutTimeoutWaitsForEnter(t *testing.T) {
	pr, pw := io.Pipe()
	defer pw.Close()
	in := ReadLines(pr)
	done := make(chan struct{})
	go func() {
		WaitForEnter(in, io.Discard, 0)
		close(done)
	}()

	select {
	case <-done:
		t.Fatal("WaitForEnter returned before Enter was pressed")
	case <-time.After(200 * time.Millisecond):
	}

	io.WriteString(pw, "\n")
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("WaitForEnter didn't return after Enter")
	}
}
