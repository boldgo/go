// Copyright 2017 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package errorstest

import (
	"bytes"
	"fmt"
	"internal/testenv"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func path(file string) string {
	return filepath.Join("testdata", file)
}

func check(t *testing.T, file string) {
	t.Run(file, func(t *testing.T) {
		testenv.MustHaveGoBuild(t)
		testenv.MustHaveCGO(t)
		t.Parallel()

		contents, err := os.ReadFile(path(file))
		if err != nil {
			t.Fatal(err)
		}
		var errors []*regexp.Regexp
		for i, line := range bytes.Split(contents, []byte("\n")) {
			if bytes.HasSuffix(line, []byte("ERROR HERE")) {
				re := regexp.MustCompile(regexp.QuoteMeta(fmt.Sprintf("%s:%d:", file, i+1)))
				errors = append(errors, re)
				continue
			}

			if _, frag, ok := bytes.Cut(line, []byte("ERROR HERE: ")); ok {
				re, err := regexp.Compile(fmt.Sprintf(":%d:.*%s", i+1, frag))
				if err != nil {
					t.Errorf("Invalid regexp after `ERROR HERE: `: %#q", frag)
					continue
				}
				errors = append(errors, re)
			}

			if _, frag, ok := bytes.Cut(line, []byte("ERROR MESSAGE: ")); ok {
				re, err := regexp.Compile(string(frag))
				if err != nil {
					t.Errorf("Invalid regexp after `ERROR MESSAGE: `: %#q", frag)
					continue
				}
				errors = append(errors, re)
			}
		}
		if len(errors) == 0 {
			t.Fatalf("cannot find ERROR HERE")
		}
		expect(t, errors, file)
	})
}

func expect(t *testing.T, errors []*regexp.Regexp, files ...string) {
	dir, err := os.MkdirTemp("", filepath.Base(t.Name()))
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	dst := filepath.Join(dir, strings.TrimSuffix(files[0], ".go"))
	args := []string{"build", "-gcflags=-e", "-o=" + dst}
	for _, file := range files {
		args = append(args, path(file))
	}
	cmd := exec.Command(testenv.GoToolPath(t), args...)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Errorf("expected cgo to fail but it succeeded")
	}

	lines := bytes.Split(out, []byte("\n"))
	for _, re := range errors {
		found := false
		for _, line := range lines {
			if re.Match(line) {
				t.Logf("found match for %#q: %q", re, line)
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected error output to contain %#q", re)
		}
	}

	if t.Failed() {
		t.Logf("actual output:\n%s", out)
	}
}

func sizeofLongDouble(t *testing.T) int {
	testenv.MustHaveGoRun(t)
	testenv.MustHaveCGO(t)
	cmd := exec.Command(testenv.GoToolPath(t), "run", path("long_double_size.go"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%#q: %v:\n%s", cmd, err, out)
	}

	i, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		t.Fatalf("long_double_size.go printed invalid size: %s", out)
	}
	return i
}

func TestReportsTypeErrors(t *testing.T) {
	for _, file := range []string{
		"err1.go",
		"err2.go",
		"err5.go",
		"issue11097a.go",
		"issue11097b.go",
		"issue18452.go",
		"issue18889.go",
		"issue28721.go",
		"issue33061.go",
		"issue50710.go",
		"issue67517.go",
		"issue67707.go",
		"issue69176.go",
	} {
		check(t, file)
	}

	if sizeofLongDouble(t) > 8 {
		for _, file := range []string{
			"err4.go",
			"issue28069.go",
		} {
			check(t, file)
		}
	}
}

func TestToleratesOptimizationFlag(t *testing.T) {
	for _, cflags := range []string{
		"",
		"-O",
	} {
		t.Run(cflags, func(t *testing.T) {
			testenv.MustHaveGoBuild(t)
			testenv.MustHaveCGO(t)
			t.Parallel()

			cmd := exec.Command(testenv.GoToolPath(t), "build", path("issue14669.go"))
			cmd.Env = append(os.Environ(), "CGO_CFLAGS="+cflags)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Errorf("%#q: %v:\n%s", cmd, err, out)
			}
		})
	}
}

func TestMallocCrashesOnNil(t *testing.T) {
	testenv.MustHaveCGO(t)
	testenv.MustHaveGoRun(t)
	t.Parallel()

	cmd := exec.Command(testenv.GoToolPath(t), "run", path("malloc.go"))
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Logf("%#q:\n%s", cmd, out)
		t.Fatalf("succeeded unexpectedly")
	}
}

func TestNotMatchedCFunction(t *testing.T) {
	check(t, "notmatchedcfunction.go")
	check(t, "notmatchednonblocking.go")
}

func TestIncompatibleDeclarations(t *testing.T) {
	testenv.MustHaveCGO(t)
	testenv.MustHaveGoRun(t)
	t.Parallel()
	expect(t, []*regexp.Regexp{
		regexp.MustCompile("inconsistent definitions for C[.]f"),
		regexp.MustCompile("inconsistent definitions for C[.]g"),
	}, "issue67699a.go", "issue67699b.go")
}

func TestPointerCheckElided(t *testing.T) {
	testenv.MustHaveGoBuild(t)
	testenv.MustHaveCGO(t)
	t.Parallel()

	cmd := exec.Command(testenv.GoToolPath(t), "build", "-o", filepath.Join(t.TempDir(), "ptrelide"), "-gcflags=-S", path("ptrelide.go"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%#q: %v:\n%s", cmd, err, out)
	}
	// -S prints each function as "main.name STEXT ..." followed by its
	// instructions. A check sits in the closure cmd/cgo wraps the call
	// in, so count a closure's calls for the function enclosing it.
	calls := map[string]int{}
	var fn string
	for line := range strings.Lines(string(out)) {
		if i := strings.Index(line, " STEXT"); i > 0 && !strings.HasPrefix(line, "\t") {
			fn = strings.TrimPrefix(line[:i], "main.")
			if j := strings.Index(fn, ".func"); j > 0 {
				fn = fn[:j]
			}
			calls[fn] += 0
			continue
		}
		if strings.Contains(line, "CALL") && strings.Contains(line, "runtime.cgoCheckPointer") {
			calls[fn]++
		}
	}
	for _, name := range []string{"bytesElided", "arrayElided", "fieldElided", "sliceElided", "pointersKept", "fieldKept", "bareKept"} {
		n, ok := calls[name]
		switch {
		case !ok:
			t.Errorf("no assembly found for %s", name)
		case strings.HasSuffix(name, "Elided") && n != 0:
			t.Errorf("%s calls runtime.cgoCheckPointer %d times, want 0", name, n)
		case strings.HasSuffix(name, "Kept") && n == 0:
			t.Errorf("%s does not call runtime.cgoCheckPointer", name)
		}
	}
}
