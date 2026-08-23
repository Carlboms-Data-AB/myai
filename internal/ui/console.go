// Package ui is MyAI's terminal interface. It renders what the core reports
// and collects what the core asks for, and it is the only package that reads
// standard input or writes to a terminal.
package ui

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"golang.org/x/term"

	"github.com/Carlboms-Data-AB/myai/internal/platform"
)

// Console renders to a terminal. It satisfies both progress.Reporter and
// prompt.Asker.
type Console struct {
	// Out is where output goes.
	Out io.Writer
	// In is where answers are read from.
	In io.Reader

	reader      *bufio.Reader
	interactive bool
	lastLabel   string
}

// NewConsole returns a Console wired to the process streams.
func NewConsole() *Console {
	return &Console{
		Out:         os.Stdout,
		In:          os.Stdin,
		reader:      bufio.NewReader(os.Stdin),
		interactive: term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd())),
	}
}

// NewTestConsole returns a Console reading and writing the given streams,
// treated as non-interactive.
func NewTestConsole(in io.Reader, out io.Writer) *Console {
	return &Console{Out: out, In: in, reader: bufio.NewReader(in)}
}

// SetInteractive overrides the detected interactivity.
func (c *Console) SetInteractive(v bool) { c.interactive = v }

func (c *Console) printf(format string, args ...any) {
	fmt.Fprintf(c.Out, format, args...)
}

// --- progress.Reporter ---

// Step announces a new stage of work.
func (c *Console) Step(message string) {
	c.clearDownload()
	c.printf("\n%s\n", message)
}

// Info reports a detail within the current stage.
func (c *Console) Info(message string) {
	c.clearDownload()
	if strings.TrimSpace(message) == "" {
		return
	}
	c.printf("  %s\n", message)
}

// Warn reports something that did not stop the operation.
func (c *Console) Warn(message string) {
	c.clearDownload()
	c.printf("  warning: %s\n", message)
}

// Check reports the outcome of a named verification.
func (c *Console) Check(name string, ok bool, detail string) {
	c.clearDownload()
	mark := "x"
	if ok {
		mark = "ok"
	}
	c.printf("  %-4s %-22s %s\n", mark, name, detail)
}

// Download reports byte progress, rewriting a single line as it goes.
func (c *Console) Download(name string, done, total int64) {
	if !c.interactive {
		return
	}
	c.lastLabel = name

	if total > 0 {
		percent := float64(done) / float64(total) * 100
		c.printf("\r  %s  %s / %s  %5.1f%%   ", name, platform.HumanBytes(done), platform.HumanBytes(total), percent)
		return
	}
	c.printf("\r  %s  %s   ", name, platform.HumanBytes(done))
}

// clearDownload ends an in-place progress line before printing something else.
func (c *Console) clearDownload() {
	if c.lastLabel == "" {
		return
	}
	c.lastLabel = ""
	if c.interactive {
		c.printf("\n")
	}
}

// --- prompt.Asker ---

// Interactive reports whether questions can be answered.
func (c *Console) Interactive() bool { return c.interactive }

// ReadLine reads one line of input.
func (c *Console) ReadLine() (string, error) {
	if c.reader == nil {
		c.reader = bufio.NewReader(c.In)
	}
	line, err := c.reader.ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

// Confirm asks a yes or no question.
func (c *Console) Confirm(question string, defaultYes bool) (bool, error) {
	suffix := "[y/N]"
	if defaultYes {
		suffix = "[Y/n]"
	}
	c.printf("%s %s ", question, suffix)

	answer, err := c.ReadLine()
	if err != nil {
		return defaultYes, nil
	}
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "":
		return defaultYes, nil
	case "y", "yes":
		return true, nil
	default:
		return false, nil
	}
}

// Text asks for a line of input.
func (c *Console) Text(question, defaultValue string) (string, error) {
	if defaultValue != "" {
		c.printf("%s [%s]: ", question, defaultValue)
	} else {
		c.printf("%s: ", question)
	}
	answer, err := c.ReadLine()
	if err != nil {
		return defaultValue, nil
	}
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return defaultValue, nil
	}
	return answer, nil
}

// Secret asks for input that is not echoed.
func (c *Console) Secret(question string) (string, error) {
	c.printf("%s: ", question)

	if f, ok := c.In.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		value, err := term.ReadPassword(int(f.Fd()))
		c.printf("\n")
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(value)), nil
	}

	// Without a terminal there is nothing to switch off, so read plainly.
	answer, err := c.ReadLine()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(answer), nil
}

// Choose asks the operator to pick one of several options.
func (c *Console) Choose(question string, options []string, defaultIndex int) (int, error) {
	c.printf("\n%s\n\n", question)
	for i, option := range options {
		c.printf(" %2d  %s\n", i+1, option)
	}
	c.printf("\nchoose [%d]: ", defaultIndex+1)

	answer, err := c.ReadLine()
	if err != nil {
		return defaultIndex, nil
	}
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return defaultIndex, nil
	}
	n, err := strconv.Atoi(answer)
	if err != nil || n < 1 || n > len(options) {
		return 0, fmt.Errorf("choose a number between 1 and %d", len(options))
	}
	return n - 1, nil
}

// --- rendering ---

// Heading prints a section title.
func (c *Console) Heading(title string) {
	c.clearDownload()
	c.printf("\n%s\n\n", title)
}

// Field prints an aligned label and value.
func (c *Console) Field(label, value string) {
	c.printf("  %-22s %s\n", label, value)
}

// Line prints a plain line.
func (c *Console) Line(text string) {
	c.clearDownload()
	c.printf("%s\n", text)
}

// Blank prints an empty line.
func (c *Console) Blank() { c.printf("\n") }

// Error prints an error.
func (c *Console) Error(err error) {
	c.clearDownload()
	fmt.Fprintf(c.Out, "\nerror: %v\n", err)
}

// Clear clears the screen when attached to a terminal.
func (c *Console) Clear() {
	if c.interactive {
		c.printf("\033[H\033[2J")
	}
}

// Pause waits for the operator to acknowledge, so output survives a screen
// clear.
func (c *Console) Pause() {
	if !c.interactive {
		return
	}
	c.printf("\n[enter] ")
	c.ReadLine()
}

// YesNo renders a boolean the way the menus display settings.
func YesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}

// EnabledDisabled renders a boolean as a feature state.
func EnabledDisabled(v bool) string {
	if v {
		return "enabled"
	}
	return "disabled"
}
