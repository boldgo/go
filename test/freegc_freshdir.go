// errorcheckdir -0 -d=freegcssa=2,freegcfresh=1

//go:build goexperiment.runtimefreegc && (amd64 || arm64)

// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Test that results proven fresh in one package are owned by callers in
// another.

package ignored
