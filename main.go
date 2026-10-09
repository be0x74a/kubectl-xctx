package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"
)

// version is set via -ldflags at build time.
var version = "dev"

// kubectlRunner executes kubectl with the given args. Overridable in tests.
var kubectlRunner = func(ctx context.Context, args ...string) (stdout, stderr []byte, err error) {
	cmd := exec.CommandContext(ctx, "kubectl", args...)
	var outBuf, errBuf strings.Builder
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err = cmd.Run()
	return []byte(outBuf.String()), []byte(errBuf.String()), err
}

// streamRunner executes kubectl, copying output to out/errOut as it arrives.
// Overridable in tests.
var streamRunner = func(ctx context.Context, args []string, out, errOut io.Writer) error {
	cmd := exec.CommandContext(ctx, "kubectl", args...)
	cmd.Stdout = out
	cmd.Stderr = errOut
	return cmd.Run()
}

func main() {
	if err := newCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func newCmd() *cobra.Command {
	var parallel bool
	var stream bool
	var list bool
	var timeout time.Duration
	var failFast bool
	var header string

	cmd := &cobra.Command{
		Use:     "kubectl-xctx [flags] <pattern> [-- kubectl args...]",
		Short:   "Execute kubectl commands across multiple contexts",
		Version: version,
		Long: `kubectl-xctx runs a kubectl command across all Kubernetes contexts
whose name matches a regular expression, printing a labeled header
for each context's output.

Modes:
  (default)   one context at a time, output written live under each header
  --parallel  all contexts concurrently, output buffered and grouped per context
  --stream    all contexts concurrently, output written live with each line
              prefixed by [context]; for logs -f, get -w and similar

xctx flags must come before the pattern; everything after the pattern
is passed directly to kubectl.

Examples:
  kubectl xctx "prod" get pods
  kubectl xctx --parallel "staging|dev" get nodes
  kubectl xctx --stream "prod" logs -f -l app=my-app
  kubectl xctx --stream --timeout 30s "prod" get pods -w
  kubectl xctx --timeout 10s "." get pods
  kubectl xctx --list "prod"
  kubectl xctx "prod" get pods -n kube-system
  kubectl xctx --header "=== {context} ===" "prod" get pods
  kubectl xctx --header "" "prod" get pods -o json | jq .`,
		Args:          cobra.MinimumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(_ *cobra.Command, args []string) error {
			if stream && parallel {
				return errors.New("--stream and --parallel cannot be used together (--stream already runs all contexts concurrently)")
			}
			if stream && failFast {
				return errors.New("--fail-fast cannot be used with --stream (it only applies to sequential mode)")
			}
			return execute(args[0], args[1:], parallel, stream, list, timeout, failFast, header)
		},
	}

	cmd.Flags().BoolVarP(&parallel, "parallel", "p", false, "Run across all contexts concurrently, buffering output per context")
	cmd.Flags().BoolVar(&stream, "stream", false, "Run across all contexts concurrently, streaming live output prefixed with [context] (for logs -f, get -w, ...)")
	cmd.Flags().BoolVarP(&list, "list", "l", false, "List matching contexts without executing")
	cmd.Flags().DurationVarP(&timeout, "timeout", "t", 0, "Per-context timeout (e.g. 10s, 1m). 0 = no timeout. With --stream, stops all streams after this duration")
	cmd.Flags().BoolVar(&failFast, "fail-fast", false, "Stop after first failure (sequential mode only)")
	cmd.Flags().StringVar(&header, "header", "### Context: {context}", `Header printed before each context's output. Use {context} as the placeholder. Set to "" to suppress.`)
	// Stop flag parsing at the first non-flag argument (the pattern), so that
	// kubectl flags like -n are not interpreted as xctx flags.
	cmd.Flags().SetInterspersed(false)

	cmd.ValidArgsFunction = completeArgs

	return cmd
}

// completeArgs provides shell completions for positional arguments.
// With no args yet it suggests context names; once the pattern is provided
// it delegates to kubectl's own completion for subcommands, resources, etc.
func completeArgs(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) == 0 {
		return completeContextNames(toComplete)
	}
	return completeKubectl(args[1:], toComplete)
}

// completeContextNames returns context names matching the partial input.
func completeContextNames(toComplete string) ([]string, cobra.ShellCompDirective) {
	out, _, err := kubectlRunner(context.Background(), "config", "get-contexts", "-o", "name")
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}
	var completions []string
	for _, name := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if name != "" && strings.HasPrefix(name, toComplete) {
			completions = append(completions, name)
		}
	}
	return completions, cobra.ShellCompDirectiveNoFileComp
}

// completeKubectl delegates completion to kubectl by calling
// "kubectl __complete <args...> <toComplete>" and parsing its output.
func completeKubectl(args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	completeArgs := append([]string{"__complete"}, args...)
	completeArgs = append(completeArgs, toComplete)
	out, _, err := kubectlRunner(context.Background(), completeArgs...)
	if err != nil {
		return nil, cobra.ShellCompDirectiveDefault
	}

	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	if len(lines) == 0 {
		return nil, cobra.ShellCompDirectiveDefault
	}

	// Last line is the cobra directive (e.g. ":4"), preceding lines are completions.
	directive := cobra.ShellCompDirectiveDefault
	last := lines[len(lines)-1]
	if strings.HasPrefix(last, ":") {
		if v, err := strconv.Atoi(last[1:]); err == nil {
			directive = cobra.ShellCompDirective(v)
		}
		lines = lines[:len(lines)-1]
	}

	return lines, directive
}

type result struct {
	ctxName string
	stdout  []byte
	stderr  []byte
	err     error
}

func execute(pattern string, kubectlArgs []string, parallel, stream, list bool, timeout time.Duration, failFast bool, header string) error {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return fmt.Errorf("invalid pattern %q: %w", pattern, err)
	}

	contexts, err := matchingContexts(re)
	if err != nil {
		return err
	}

	if len(contexts) == 0 {
		fmt.Fprintf(os.Stderr, "no contexts matched pattern %q\n", pattern)
		return nil
	}

	if list {
		for _, c := range contexts {
			fmt.Println(c)
		}
		return nil
	}

	if len(kubectlArgs) == 0 {
		return fmt.Errorf("no kubectl command provided (use -- to separate kubectl args, e.g. kubectl xctx \"prod\" -- get pods)")
	}

	if stream {
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		if timeout > 0 {
			var cancelTimeout context.CancelFunc
			ctx, cancelTimeout = context.WithTimeout(ctx, timeout)
			defer cancelTimeout()
		}
		return runStreaming(ctx, contexts, kubectlArgs, header, os.Stdout, os.Stderr)
	}

	if parallel {
		return runParallel(contexts, kubectlArgs, timeout, header, os.Stdout, os.Stderr)
	}
	return runSequential(contexts, kubectlArgs, timeout, failFast, header, os.Stdout, os.Stderr)
}

func matchingContexts(re *regexp.Regexp) ([]string, error) {
	out, _, err := kubectlRunner(context.Background(), "config", "get-contexts", "-o", "name")
	if err != nil {
		return nil, fmt.Errorf("failed to list kubectl contexts: %w", err)
	}

	var matched []string
	for _, name := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if name != "" && re.MatchString(name) {
			matched = append(matched, name)
		}
	}
	return matched, nil
}

func runInContext(ctx context.Context, ctxName string, args []string) result {
	stdout, stderr, err := kubectlRunner(ctx, append([]string{"--context", ctxName}, args...)...)
	return result{ctxName: ctxName, stdout: stdout, stderr: stderr, err: err}
}

func printResult(r result, header string, out, errOut io.Writer) {
	if header != "" {
		_, _ = fmt.Fprintln(out, strings.ReplaceAll(header, "{context}", r.ctxName))
	}
	_, _ = out.Write(r.stdout)
	if len(r.stderr) > 0 {
		_, _ = errOut.Write(r.stderr)
	}
	if r.err != nil {
		_, _ = fmt.Fprintf(errOut, "[xctx] context %q failed: %v\n", r.ctxName, r.err)
	}
	if header != "" {
		_, _ = fmt.Fprintln(out)
	}
}

func runSequential(contexts, kubectlArgs []string, timeout time.Duration, failFast bool, header string, out, errOut io.Writer) error {
	var failed int
	for _, ctxName := range contexts {
		if header != "" {
			_, _ = fmt.Fprintln(out, strings.ReplaceAll(header, "{context}", ctxName))
		}
		ctx, cancel := maybeWithTimeout(timeout)
		err := streamRunner(ctx, append([]string{"--context", ctxName}, kubectlArgs...), out, errOut)
		cancel()
		if err != nil {
			_, _ = fmt.Fprintf(errOut, "[xctx] context %q failed: %v\n", ctxName, err)
		}
		if header != "" {
			_, _ = fmt.Fprintln(out)
		}
		if err != nil {
			failed++
			if failFast {
				return fmt.Errorf("stopped after failure in context %q (%d context(s) failed)", ctxName, failed)
			}
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d context(s) failed", failed)
	}
	return nil
}

func runParallel(contexts, kubectlArgs []string, timeout time.Duration, header string, out, errOut io.Writer) error {
	results := make([]result, len(contexts))
	var wg sync.WaitGroup
	for i, ctxName := range contexts {
		wg.Add(1)
		go func(i int, ctxName string) {
			defer wg.Done()
			ctx, cancel := maybeWithTimeout(timeout)
			defer cancel()
			results[i] = runInContext(ctx, ctxName, kubectlArgs)
		}(i, ctxName)
	}
	wg.Wait()

	var failed int
	for _, r := range results {
		printResult(r, header, out, errOut)
		if r.err != nil {
			failed++
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d context(s) failed", failed)
	}
	return nil
}

func maybeWithTimeout(d time.Duration) (context.Context, context.CancelFunc) {
	if d > 0 {
		return context.WithTimeout(context.Background(), d)
	}
	return context.Background(), func() {}
}

type streamWriter struct {
	out     io.Writer
	mu      *sync.Mutex
	prefix  string
	pending []byte
}

func (w *streamWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.pending = append(w.pending, data...)
	for {
		newline := bytes.IndexByte(w.pending, '\n')
		if newline < 0 {
			break
		}
		if err := w.writeLine(w.pending[:newline+1]); err != nil {
			return 0, err
		}
		w.pending = w.pending[newline+1:]
	}
	return len(data), nil
}

func (w *streamWriter) writeLine(line []byte) error {
	if _, err := io.WriteString(w.out, w.prefix); err != nil {
		return err
	}
	count, err := w.out.Write(line)
	if err == nil && count != len(line) {
		return io.ErrShortWrite
	}
	return err
}

func (w *streamWriter) flush() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.pending) == 0 {
		return nil
	}
	err := w.writeLine(w.pending)
	w.pending = nil
	return err
}

// cancelGrace is how long a failed stream waits to see whether ctx is about to
// be cancelled. kubectl gets the terminal's SIGINT too and may exit first.
var cancelGrace = 100 * time.Millisecond

// runStreaming runs all contexts concurrently with live, line-prefixed output.
// Streams ended by ctx (Ctrl+C or --timeout) are not counted as failures.
func runStreaming(ctx context.Context, contexts, kubectlArgs []string, header string, out, errOut io.Writer) error {
	var mu sync.Mutex
	var wg sync.WaitGroup
	errors := make([]error, len(contexts))
	for index, ctxName := range contexts {
		wg.Add(1)
		go func(index int, ctxName string) {
			defer wg.Done()
			var prefix string
			if header != "" {
				prefix = "[" + ctxName + "] "
			}
			stdout := &streamWriter{out: out, mu: &mu, prefix: prefix}
			stderr := &streamWriter{out: errOut, mu: &mu, prefix: prefix}
			errors[index] = streamRunner(ctx, append([]string{"--context", ctxName}, kubectlArgs...), stdout, stderr)
			for _, writer := range []*streamWriter{stdout, stderr} {
				if err := writer.flush(); errors[index] == nil {
					errors[index] = err
				}
			}
			if errors[index] != nil && ctx.Err() == nil {
				select {
				case <-ctx.Done():
				case <-time.After(cancelGrace):
				}
			}
			if ctx.Err() != nil {
				errors[index] = nil
			}
			if errors[index] != nil {
				mu.Lock()
				_, _ = fmt.Fprintf(errOut, "[xctx] context %q failed: %v\n", ctxName, errors[index])
				mu.Unlock()
			}
		}(index, ctxName)
	}
	wg.Wait()
	var failed int
	for _, err := range errors {
		if err != nil {
			failed++
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d context(s) failed", failed)
	}
	return nil
}
