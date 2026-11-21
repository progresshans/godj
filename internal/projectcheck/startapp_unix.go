//go:build darwin || linux

package projectcheck

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"

	"github.com/progresshans/godj/internal/projectscaffold"
)

type StartAppInvocation struct {
	Context        context.Context
	CWD            string
	Args           []string
	Stdout, Stderr io.Writer
	Interrupt      <-chan struct{}
}

type StartAppReport struct {
	ExitCode    int
	FailureCode string
	Publication projectscaffold.Result
}

// RunStartApp creates an editable app declaration and installation guidance.
// It has no implicit project/settings discovery or application initialization.
// Installing an app is a separate explicit ProjectSpec declaration change.
func RunStartApp(input StartAppInvocation) StartAppReport {
	input.Args = append([]string(nil), input.Args...)
	if input.Context == nil {
		input.Context = context.Background()
	}
	report := StartAppReport{}
	fail := func(code string, exit int) StartAppReport {
		report.FailureCode, report.ExitCode = code, exit
		if input.Stderr != nil {
			_, _ = io.WriteString(input.Stderr, "startapp_error/"+code+"\n")
		}
		return report
	}
	if len(input.Args) != 2 && len(input.Args) != 3 || input.Args[0] != "startapp" || projectscaffold.ValidateAppLabel(input.Args[1]) != nil {
		return fail("invalid_arguments", 2)
	}
	destination := input.Args[1]
	if len(input.Args) == 3 {
		destination = input.Args[2]
	}
	if !scaffoldPath(destination) {
		return fail("invalid_arguments", 2)
	}
	if input.Stdout == nil {
		return fail("output_unavailable", 1)
	}
	if code := commandInterruption(input.Context, input.Interrupt); code != "" {
		return fail(code, 130)
	}
	cwd := input.CWD
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return fail("working_directory_unavailable", 1)
		}
	}
	if !filepath.IsAbs(cwd) {
		return fail("invalid_working_directory", 2)
	}
	if !filepath.IsAbs(destination) {
		destination = filepath.Join(cwd, destination)
	}
	ctx, join := commandInterruptContext(input.Context, input.Interrupt)
	defer join()
	publication, err := projectscaffold.PublishApp(ctx, destination, projectscaffold.App{Label: input.Args[1]})
	report.Publication = publication
	if err != nil {
		code, exit := scaffoldFailure(input.Context, input.Interrupt, publication, err)
		return fail(code, exit)
	}
	document, _ := json.Marshal(struct {
		FormatVersion int    `json:"format_version"`
		Status        string `json:"status"`
		AppLabel      string `json:"app_label"`
		Directory     string `json:"directory"`
		Installed     bool   `json:"installed"`
	}{1, "created", input.Args[1], publication.Directory, false})
	document = append(document, '\n')
	if n, err := input.Stdout.Write(document); err != nil || n != len(document) {
		return fail("published_output_failed", 1)
	}
	return report
}
