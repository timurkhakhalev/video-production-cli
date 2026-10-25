// Package ffmpeg runs ffmpeg/ffprobe and parses their output.
package ffmpeg

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// Bin names, overridable for tests.
var (
	FFmpeg  = "ffmpeg"
	FFprobe = "ffprobe"
)

// Runner executes external commands; argv[0] is the binary. Tests swap R for a fake.
type Runner interface {
	// Run lets the command print to the terminal (progress on stderr) and fails on a non-zero exit.
	Run(argv []string) error
	// Output returns stdout; stderr goes into the error.
	Output(argv []string) (string, error)
	// Stderr returns stderr regardless of the exit code (filters like silencedetect report there).
	Stderr(argv []string) (string, error)
}

// R is the runner used by every package.
var R Runner = ExecRunner{}

// ExecRunner runs real processes.
type ExecRunner struct{}

func (ExecRunner) Run(argv []string) error {
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s failed: %v", argv[0], err)
	}
	return nil
}

func (ExecRunner) Output(argv []string) (string, error) {
	var stdout, stderr bytes.Buffer
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s failed: %v: %s", argv[0], err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

func (ExecRunner) Stderr(argv []string) (string, error) {
	var stderr bytes.Buffer
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stderr = &stderr
	err := cmd.Run()
	if _, ok := err.(*exec.ExitError); ok {
		err = nil
	}
	return stderr.String(), err
}

// Run, Output and Stderr call R.
func Run(argv ...string) error              { return R.Run(argv) }
func Output(argv ...string) (string, error) { return R.Output(argv) }
func Stderr(argv ...string) (string, error) { return R.Stderr(argv) }

var versionRe = regexp.MustCompile(`version (?:n)?(\d+)\.(\d+)`)

// Version returns the major.minor of an ffmpeg-family binary and the first output line.
func Version(name string) (major, minor int, line string, err error) {
	out, err := Output(name, "-version")
	if err != nil {
		return 0, 0, "", err
	}
	line, _, _ = strings.Cut(out, "\n")
	m := versionRe.FindStringSubmatch(line)
	if m == nil {
		return 0, 0, line, nil // git builds: unknown version
	}
	major, _ = strconv.Atoi(m[1])
	minor, _ = strconv.Atoi(m[2])
	return major, minor, line, nil
}

// MissingFilters reports which of the given filter names ffmpeg lacks.
func MissingFilters(names ...string) ([]string, error) {
	out, err := Output(FFmpeg, "-hide_banner", "-filters")
	if err != nil {
		return nil, err
	}
	have := map[string]bool{}
	for _, l := range strings.Split(out, "\n") {
		f := strings.Fields(l)
		if len(f) >= 2 {
			have[f[1]] = true
		}
	}
	var missing []string
	for _, n := range names {
		if !have[n] {
			missing = append(missing, n)
		}
	}
	return missing, nil
}
