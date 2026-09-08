package main

import (
	"bytes"
	"errors"
	"flag"
	"io"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"

	"github.com/RandomCodeSpace/kb/internal/webui"
)

// stubWebRun replaces the webui entry point and build info for one test and
// returns the options the stub received.
func stubWebRun(t *testing.T) *webui.Options {
	t.Helper()
	originalRun := webRun
	originalBuildInfo := readBuildInfo
	t.Cleanup(func() {
		webRun = originalRun
		readBuildInfo = originalBuildInfo
	})
	readBuildInfo = func() (*debug.BuildInfo, bool) {
		return &debug.BuildInfo{Main: debug.Module{Version: "v9.8.7"}}, true
	}
	got := &webui.Options{}
	webRun = func(opts webui.Options, stdout, stderr io.Writer) error {
		*got = opts
		if stdout != rootStdout || stderr != rootStderr {
			t.Errorf("webRun streams = %v, %v; want rootStdout, rootStderr", stdout, stderr)
		}
		return nil
	}
	return got
}

func TestRunWebPassesResolvedFlags(t *testing.T) {
	envData := filepath.Join(t.TempDir(), "env")
	flagData := filepath.Join(t.TempDir(), "flag")
	t.Setenv("KB_DATA", envData)

	base := webui.Options{DataDir: envData, User: defaultBoardUser, Version: "v9.8.7", Addr: "127.0.0.1:0", OpenBrowser: true}
	cases := []struct {
		name string
		args []string
		want func(webui.Options) webui.Options
	}{
		{name: "defaults", args: nil, want: func(o webui.Options) webui.Options { return o }},
		{name: "data", args: []string{"--data", flagData}, want: func(o webui.Options) webui.Options {
			o.DataDir = flagData
			return o
		}},
		{name: "addr", args: []string{"--addr", "127.0.0.1:8321"}, want: func(o webui.Options) webui.Options {
			o.Addr = "127.0.0.1:8321"
			return o
		}},
		{name: "no-open", args: []string{"--no-open"}, want: func(o webui.Options) webui.Options {
			o.OpenBrowser = false
			return o
		}},
		{name: "unsafe-listen", args: []string{"--unsafe-listen", "--addr", "0.0.0.0:8321"}, want: func(o webui.Options) webui.Options {
			o.AllowRemote = true
			o.Addr = "0.0.0.0:8321"
			return o
		}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got := stubWebRun(t)
			if err := runWeb(tt.args); err != nil {
				t.Fatalf("runWeb(%v): %v", tt.args, err)
			}
			if want := tt.want(base); *got != want {
				t.Fatalf("web options = %+v, want %+v", *got, want)
			}
		})
	}
}

func TestRunWebRejectsBadInput(t *testing.T) {
	got := stubWebRun(t)
	t.Setenv("KB_DATA", t.TempDir())

	err := runWeb([]string{"extra"})
	if err == nil || err.Error() != "web takes no arguments" {
		t.Fatalf("positional argument error = %v", err)
	}

	var flagOutput bytes.Buffer
	err = runWebWithFlagOutput([]string{"--user", "alice"}, &flagOutput)
	var flagErr *commandFlagError
	if !errors.As(err, &flagErr) || !strings.Contains(err.Error(), "flag provided but not defined: -user") {
		t.Fatalf("unknown flag error = %v", err)
	}
	if !strings.Contains(flagOutput.String(), "kb web") {
		t.Fatalf("flag usage output = %q, want kb web usage", flagOutput.String())
	}

	err = runWebWithFlagOutput([]string{"-h"}, &flagOutput)
	if !errors.As(err, &flagErr) || !errors.Is(flagErr, flag.ErrHelp) {
		t.Fatalf("-h error = %v, want commandFlagError wrapping flag.ErrHelp", err)
	}
	if got.DataDir != "" {
		t.Fatalf("webRun ran after a usage error with %+v", *got)
	}

	webRun = func(webui.Options, io.Writer, io.Writer) error { return errors.New("serve failed") }
	if err := runWeb(nil); err == nil || err.Error() != "serve failed" {
		t.Fatalf("runWeb error = %v, want serve failed", err)
	}
}
