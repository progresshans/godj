//go:build darwin || linux

package main

import (
	"context"
	"io"
	"strings"
)

func runHelp(ctx context.Context, args []string, stdout, stderr io.Writer, interrupt <-chan struct{}) (bool, int) {
	if len(args) == 0 || args[0] != "help" && args[0] != "--help" {
		return false, 0
	}
	usage := []string{
		"startproject MODULE DIRECTORY [--framework-version VERSION | --framework-source PATH]",
		"startapp LABEL [DIRECTORY]",
		"version [--json]",
		"generate [--check] [--project PATH]",
		"migrations check [--project PATH]",
		"makemigrations [--project PATH]",
		"migrate [--plan] [--project PATH]",
		"showmigrations [--project PATH]",
		"sqlmigrate APP NAME [--project PATH]",
		"createsuperuser [--project PATH]",
		"runserver [--listen ADDRESS] [--project PATH]",
		"check [--project PATH] [--list | --resource NAME ...]",
		"commands [--project PATH]",
		"command [--project PATH] NAME [ARGUMENTS]",
		"help [--project PATH] [NAME]",
	}
	var lines []string
	if len(args) == 1 {
		lines = usage
	} else if args[0] == "help" && len(args) == 2 {
		for _, line := range usage {
			if strings.Fields(line)[0] == args[1] {
				lines = []string{line}
				break
			}
		}
	}
	if len(lines) == 0 {
		return false, 0
	}
	if ctx == nil || stdout == nil || stderr == nil {
		return true, 2
	}
	select {
	case <-interrupt:
		return true, 1
	default:
	}
	if ctx.Err() != nil {
		return true, 1
	}
	document := "GoDj commands:\n  godj " + strings.Join(lines, "\n  godj ") + "\n"
	n, err := io.WriteString(stdout, document)
	if err != nil || n != len(document) {
		_, _ = io.WriteString(stderr, "help_error/output_failed\n")
		return true, 1
	}
	return true, 0
}
