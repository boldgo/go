// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package test

import (
	"internal/buildcfg"
	"internal/testenv"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestFreshResultsExported checks that a package built by the go command
// sees which results of an imported function are fresh. The go command
// asks the compiler for its export data early, and the facts come from
// the backend.
func TestFreshResultsExported(t *testing.T) {
	if !buildcfg.Experiment.RuntimeFreegc {
		t.Skip("requires GOEXPERIMENT=runtimefreegc")
	}
	testenv.MustHaveGoBuild(t)
	t.Parallel()

	dir := t.TempDir()
	files := map[string]string{
		"go.mod": "module example\n",
		"a/a.go": `package a

//go:noinline
func Itoa(n int) string {
	var b [20]byte
	i := len(b)
	for {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
		if n == 0 {
			break
		}
	}
	return string(b[i:])
}
`,
		"b/b.go": `package b

import "example/a"

func Label(n int, xs []string) string {
	s := a.Itoa(n)
	for _, x := range xs {
		s += x
	}
	return s
}
`,
	}
	for name, src := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o777); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(src), 0o666); err != nil {
			t.Fatal(err)
		}
	}

	cmd := testenv.Command(t, testenv.GoToolPath(t), "build", "-gcflags=example/b=-d=freegcssa=2", "./b")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	for line := range strings.Lines(string(out)) {
		if strings.Contains(line, "b.go:8:") && strings.Contains(line, "ssa ownership: frees old string") {
			return
		}
	}
	t.Errorf("s += x does not free the string from a.Itoa; output:\n%s", out)
}
