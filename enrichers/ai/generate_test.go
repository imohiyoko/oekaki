package ai

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/imohiyoko/oekaki/core"
)

func TestGenerateUsesValidatedStdout(t *testing.T) {
	d := t.TempDir()
	command := filepath.Join(d, "generator")
	args := []string(nil)
	var src []byte
	if runtime.GOOS == "windows" {
		command += ".bat"
		comspec := os.Getenv("ComSpec")
		if comspec == "" {
			comspec = "cmd.exe"
		}
		args = []string{"/d", "/c", command}
		src = []byte("@echo off\r\necho {\"kind\":\"oekaki.ai-candidates\",\"version\":\"1\",\"candidates\":[]}\r\n")
		command = comspec
	} else {
		src = []byte("#!/bin/sh\ncat >/dev/null\nprintf '%s' '{\"kind\":\"oekaki.ai-candidates\",\"version\":\"1\",\"candidates\":[]}'\n")
	}
	script := filepath.Join(d, "generator")
	if runtime.GOOS == "windows" {
		script += ".bat"
	}
	if err := os.WriteFile(script, src, 0700); err != nil {
		t.Fatal(err)
	}
	g := core.New()
	if runtime.GOOS == "windows" {
		args[2] = script
	}
	if _, err := Generate(context.Background(), command, args, g); err != nil {
		t.Fatal(err)
	}
}
