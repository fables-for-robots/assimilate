package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"syscall"
	"testing"
	"time"

	"github.com/urfave/cli/v2"
)

func TestReorderArgs(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   []string
		want []string
	}{
		{
			name: "trailing flag after positional",
			in:   []string{"assimilate", "deploy", "staging", "--rollout"},
			want: []string{"assimilate", "deploy", "--rollout", "staging"},
		},
		{
			name: "mixed flags stay in order",
			in:   []string{"assimilate", "deploy", "--plain", "staging", "--rollout"},
			want: []string{"assimilate", "deploy", "--plain", "--rollout", "staging"},
		},
		{
			name: "no flags untouched",
			in:   []string{"assimilate", "render", "staging"},
			want: []string{"assimilate", "render", "staging"},
		},
		{
			name: "global flag is not a subcommand",
			in:   []string{"assimilate", "--help"},
			want: []string{"assimilate", "--help"},
		},
		{
			name: "terminator freezes everything after it",
			in:   []string{"assimilate", "deploy", "--", "staging", "--rollout"},
			want: []string{"assimilate", "deploy", "--", "staging", "--rollout"},
		},
		{
			name: "bare invocation",
			in:   []string{"assimilate"},
			want: []string{"assimilate"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := reorderArgs(tc.in); !slices.Equal(got, tc.want) {
				t.Errorf("reorderArgs(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestAppParsesTrailingRolloutFlag drives the real cli.App with reordered
// args: the documented `assimilate deploy staging --rollout` must parse
// --rollout as a flag and leave exactly one positional.
func TestAppParsesTrailingRolloutFlag(t *testing.T) {
	app := newApp()
	var called bool
	for _, cmd := range app.Commands {
		if cmd.Name == "deploy" {
			cmd.Action = func(c *cli.Context) error {
				called = true
				if !c.Bool("rollout") {
					t.Error("rollout flag not parsed")
				}
				if c.NArg() != 1 {
					t.Errorf("NArg = %d, want 1 (args %q)", c.NArg(), c.Args().Slice())
				}
				if c.Args().First() != "staging" {
					t.Errorf("first arg = %q, want %q", c.Args().First(), "staging")
				}
				return nil
			}
		}
	}
	args := reorderArgs([]string{"assimilate", "deploy", "staging", "--rollout"})
	if err := app.Run(args); err != nil {
		t.Fatalf("app.Run(%q): %v", args, err)
	}
	if !called {
		t.Fatal("deploy action never ran")
	}
}

func TestSignalContextFirstSignalCancels(t *testing.T) {
	ctx, stop := signalContext(context.Background())
	defer stop()
	if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("ctx not cancelled after SIGINT")
	}
}

// TestSignalContextSecondSignalKills re-executes the test binary as a child
// that holds a signalContext: the first SIGINT must cancel ctx (child prints
// "draining"), after which the released registration must let a further
// SIGINT terminate the child instead of being swallowed for the whole drain.
func TestSignalContextSecondSignalKills(t *testing.T) {
	if os.Getenv("ASSIMILATE_SIGNAL_CHILD") == "1" {
		ctx, stop := signalContext(context.Background())
		defer stop()
		fmt.Println("ready")
		<-ctx.Done()
		fmt.Println("draining")
		time.Sleep(30 * time.Second) // simulated drain; a signal must cut it short
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=TestSignalContextSecondSignalKills$")
	cmd.Env = append(os.Environ(), "ASSIMILATE_SIGNAL_CHILD=1")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()

	sc := bufio.NewScanner(stdout)
	waitLine := func(want string) {
		for sc.Scan() {
			if sc.Text() == want {
				return
			}
		}
		t.Fatalf("child exited before printing %q", want)
	}
	waitLine("ready")
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	waitLine("draining")

	// The release goroutine runs just after ctx.Done(), so retry the second
	// signal until one lands on the restored default disposition.
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	deadline := time.After(10 * time.Second)
	for {
		if err := cmd.Process.Signal(os.Interrupt); err != nil {
			t.Fatal(err)
		}
		select {
		case err := <-done:
			var ee *exec.ExitError
			if !errors.As(err, &ee) {
				t.Fatalf("child exited without signal status: %v", err)
			}
			ws, ok := ee.Sys().(syscall.WaitStatus)
			if !ok || !ws.Signaled() || ws.Signal() != syscall.SIGINT {
				t.Fatalf("child exit status = %v, want death by SIGINT", ee)
			}
			return
		case <-deadline:
			t.Fatal("second SIGINT swallowed; child kept draining")
		case <-time.After(100 * time.Millisecond):
		}
	}
}
