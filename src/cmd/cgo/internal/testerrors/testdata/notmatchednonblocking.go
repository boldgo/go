// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

/*
// ERROR MESSAGE: #cgo nonblocking noMatchedCFunction: no matched C function
#cgo nonblocking noMatchedCFunction
*/
import "C"

func main() {
}
