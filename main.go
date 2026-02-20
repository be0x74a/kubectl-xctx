package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
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

func main() {
	if err := newCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func newCmd() *cobra.Command {
	var parallel bool
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

xctx flags must come before the pattern; everything after the pattern
is passed directly to kubectl.

Examples:
  kubectl xctx "prod" get pods
  kubectl xctx --parallel "staging|dev" get nodes
  kubectl xctx --timeout 10s "." get pods
  kubectl xctx --list "prod"
  kubectl xctx "prod" get pods -n kube-system
  kubectl xctx --header "=== {context} ===" "prod" get pods
  kubectl xctx --header "" "prod" get pods -o json | jq .`,
		Args:          cobra.MinimumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(_ *cobra.Command, args []string) error {
			return execute(args[0], args[1:], parallel, list, timeout, failFast, header)
		},
	}

	cmd.Flags().BoolVarP(&parallel, "parallel", "p", false, "Run across all contexts concurrently")
	cmd.Flags().BoolVarP(&list, "list", "l", false, "List matching contexts without executing")
	cmd.Flags().DurationVarP(&timeout, "timeout", "t", 0, "Per-context timeout (e.g. 10s, 1m). 0 = no timeout")
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

func execute(pattern string, kubectlArgs []string, parallel, list bool, timeout time.Duration, failFast bool, header string) error {
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
		ctx, cancel := maybeWithTimeout(timeout)
		r := runInContext(ctx, ctxName, kubectlArgs)
		cancel()
		printResult(r, header, out, errOut)
		if r.err != nil {
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
