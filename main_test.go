package main

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"
)

// mockKubectl replaces kubectlRunner for the duration of the test.
func mockKubectl(t *testing.T, fn func(ctx context.Context, args ...string) ([]byte, []byte, error)) {
	t.Helper()
	orig := kubectlRunner
	kubectlRunner = fn
	t.Cleanup(func() { kubectlRunner = orig })
}

// fakeContextList is the standard set of contexts returned by the mock.
const fakeContextList = "prod-us-east\nprod-eu-west\nstaging-us\ndev-local"

// useFakeKubectl installs a mock that returns fakeContextList for get-contexts
// and "result from <ctx>\n" for any other command.
func useFakeKubectl(t *testing.T) {
	t.Helper()
	mockKubectl(t, func(_ context.Context, args ...string) ([]byte, []byte, error) {
		if len(args) >= 3 && args[0] == "config" && args[1] == "get-contexts" {
			return []byte(fakeContextList), nil, nil
		}
		if len(args) >= 2 && args[0] == "--context" {
			return []byte("result from " + args[1] + "\n"), nil, nil
		}
		return nil, nil, errors.New("unexpected fake kubectl call: " + strings.Join(args, " "))
	})
}

// --- matchingContexts ---

func TestMatchingContexts_AllMatch(t *testing.T) {
	useFakeKubectl(t)
	got, err := matchingContexts(regexp.MustCompile("."))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 4 {
		t.Errorf("want 4 contexts, got %d: %v", len(got), got)
	}
}

func TestMatchingContexts_FilterByPattern(t *testing.T) {
	useFakeKubectl(t)
	got, err := matchingContexts(regexp.MustCompile("prod"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("want 2 prod contexts, got %d: %v", len(got), got)
	}
	for _, c := range got {
		if !strings.HasPrefix(c, "prod") {
			t.Errorf("expected prod context, got %q", c)
		}
	}
}

func TestMatchingContexts_NoMatch(t *testing.T) {
	useFakeKubectl(t)
	got, err := matchingContexts(regexp.MustCompile("nonexistent"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("want 0 contexts, got %d: %v", len(got), got)
	}
}

func TestMatchingContexts_KubectlError(t *testing.T) {
	mockKubectl(t, func(_ context.Context, _ ...string) ([]byte, []byte, error) {
		return nil, nil, errors.New("kubectl not found")
	})
	_, err := matchingContexts(regexp.MustCompile("."))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

// --- printResult ---

func TestPrintResult_DefaultHeader(t *testing.T) {
	var out, errOut strings.Builder
	r := result{ctxName: "prod-us-east", stdout: []byte("pod/foo\n")}
	printResult(r, "### Context: {context}", &out, &errOut)

	if !strings.Contains(out.String(), "### Context: prod-us-east") {
		t.Errorf("expected header in output, got: %q", out.String())
	}
	if !strings.Contains(out.String(), "pod/foo") {
		t.Errorf("expected stdout content in output, got: %q", out.String())
	}
	// blank line separator should be present
	if !strings.HasSuffix(out.String(), "\n\n") {
		t.Errorf("expected trailing blank line, got: %q", out.String())
	}
}

func TestPrintResult_CustomHeader(t *testing.T) {
	var out, errOut strings.Builder
	r := result{ctxName: "staging", stdout: []byte("output\n")}
	printResult(r, "=== {context} ===", &out, &errOut)

	if !strings.Contains(out.String(), "=== staging ===") {
		t.Errorf("expected custom header, got: %q", out.String())
	}
}

func TestPrintResult_NoHeader(t *testing.T) {
	var out, errOut strings.Builder
	r := result{ctxName: "prod", stdout: []byte("{\"items\":[]}\n")}
	printResult(r, "", &out, &errOut)

	if strings.Contains(out.String(), "prod") {
		t.Errorf("expected no header, but found context name in output: %q", out.String())
	}
	// no trailing blank line when header is suppressed
	if strings.HasSuffix(out.String(), "\n\n") {
		t.Errorf("expected no trailing blank line, got: %q", out.String())
	}
}

func TestPrintResult_StderrPropagated(t *testing.T) {
	var out, errOut strings.Builder
	r := result{ctxName: "prod", stderr: []byte("Error from server\n"), err: errors.New("exit status 1")}
	printResult(r, "### Context: {context}", &out, &errOut)

	if !strings.Contains(errOut.String(), "Error from server") {
		t.Errorf("expected stderr content, got: %q", errOut.String())
	}
	if !strings.Contains(errOut.String(), "prod") {
		t.Errorf("expected failure message with context name, got: %q", errOut.String())
	}
}

// --- execute ---

func TestExecute_InvalidRegex(t *testing.T) {
	err := execute("[invalid", nil, false, false, 0, false, "")
	if err == nil {
		t.Fatal("expected error for invalid regex, got nil")
	}
}

func TestExecute_NoMatch(t *testing.T) {
	useFakeKubectl(t)
	err := execute("nonexistent", []string{"get", "pods"}, false, false, 0, false, "### Context: {context}")
	if err != nil {
		t.Errorf("expected nil error for no-match case, got: %v", err)
	}
}

func TestExecute_NoCommand(t *testing.T) {
	useFakeKubectl(t)
	err := execute("prod", nil, false, false, 0, false, "### Context: {context}")
	if err == nil {
		t.Fatal("expected error when no kubectl command given, got nil")
	}
}

func TestExecute_List(t *testing.T) {
	useFakeKubectl(t)
	var out strings.Builder
	// Intercept stdout by temporarily replacing — test list via matchingContexts directly
	re := regexp.MustCompile("prod")
	contexts, err := matchingContexts(re)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range contexts {
		out.WriteString(c + "\n")
	}
	if !strings.Contains(out.String(), "prod-us-east") || !strings.Contains(out.String(), "prod-eu-west") {
		t.Errorf("unexpected list output: %q", out.String())
	}
}

// --- runSequential ---

func TestRunSequential_AllSucceed(t *testing.T) {
	useFakeKubectl(t)
	var out, errOut strings.Builder
	err := runSequential([]string{"prod-us-east", "prod-eu-west"}, []string{"get", "pods"}, 0, false, "### Context: {context}", &out, &errOut)
	if err != nil {
		t.Errorf("expected nil, got: %v", err)
	}
	if !strings.Contains(out.String(), "prod-us-east") || !strings.Contains(out.String(), "prod-eu-west") {
		t.Errorf("expected both contexts in output, got: %q", out.String())
	}
}

func TestRunSequential_CountsFailures(t *testing.T) {
	mockKubectl(t, func(_ context.Context, args ...string) ([]byte, []byte, error) {
		if args[0] == "config" {
			return []byte(fakeContextList), nil, nil
		}
		return nil, nil, errors.New("connection refused")
	})
	var out, errOut strings.Builder
	err := runSequential([]string{"prod-us-east", "prod-eu-west"}, []string{"get", "pods"}, 0, false, "", &out, &errOut)
	if err == nil {
		t.Fatal("expected error for failed contexts, got nil")
	}
	if !strings.Contains(err.Error(), "2") {
		t.Errorf("expected failure count in error, got: %v", err)
	}
}

func TestRunSequential_FailFast(t *testing.T) {
	callCount := 0
	mockKubectl(t, func(_ context.Context, args ...string) ([]byte, []byte, error) {
		if args[0] == "config" {
			return []byte(fakeContextList), nil, nil
		}
		callCount++
		return nil, nil, errors.New("connection refused")
	})
	var out, errOut strings.Builder
	err := runSequential([]string{"ctx-a", "ctx-b", "ctx-c"}, []string{"get", "pods"}, 0, true, "", &out, &errOut)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if callCount != 1 {
		t.Errorf("fail-fast should stop after first failure, but kubectl was called %d times", callCount)
	}
}

// --- runParallel ---

func TestRunParallel_AllSucceed(t *testing.T) {
	useFakeKubectl(t)
	var out, errOut strings.Builder
	err := runParallel([]string{"prod-us-east", "prod-eu-west"}, []string{"get", "pods"}, 0, "### Context: {context}", &out, &errOut)
	if err != nil {
		t.Errorf("expected nil, got: %v", err)
	}
}

func TestRunParallel_CountsFailures(t *testing.T) {
	mockKubectl(t, func(_ context.Context, args ...string) ([]byte, []byte, error) {
		if args[0] == "config" {
			return []byte(fakeContextList), nil, nil
		}
		return nil, nil, errors.New("connection refused")
	})
	var out, errOut strings.Builder
	err := runParallel([]string{"ctx-a", "ctx-b"}, []string{"get", "pods"}, 0, "", &out, &errOut)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "2") {
		t.Errorf("expected failure count in error, got: %v", err)
	}
}

func TestRunParallel_OutputOrdering(t *testing.T) {
	// Parallel runs must print results in input order, not arrival order.
	// Simulate varying latency: first context is slower.
	mockKubectl(t, func(_ context.Context, args ...string) ([]byte, []byte, error) {
		if args[0] == "config" {
			return []byte(fakeContextList), nil, nil
		}
		if args[1] == "slow-ctx" {
			time.Sleep(20 * time.Millisecond)
		}
		return []byte("result from " + args[1] + "\n"), nil, nil
	})
	var out, errOut strings.Builder
	err := runParallel([]string{"slow-ctx", "fast-ctx"}, []string{"get", "pods"}, 0, "### Context: {context}", &out, &errOut)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	slowIdx := strings.Index(out.String(), "slow-ctx")
	fastIdx := strings.Index(out.String(), "fast-ctx")
	if slowIdx > fastIdx {
		t.Errorf("expected slow-ctx before fast-ctx (input order), but output was:\n%s", out.String())
	}
}

// --- maybeWithTimeout ---

func TestMaybeWithTimeout_Zero(t *testing.T) {
	ctx, cancel := maybeWithTimeout(0)
	defer cancel()
	select {
	case <-ctx.Done():
		t.Error("context should not be done with zero timeout")
	default:
	}
}

func TestMaybeWithTimeout_NonZero(t *testing.T) {
	ctx, cancel := maybeWithTimeout(1 * time.Millisecond)
	defer cancel()
	time.Sleep(10 * time.Millisecond)
	select {
	case <-ctx.Done():
		// expected
	default:
		t.Error("context should be done after timeout")
	}
}
