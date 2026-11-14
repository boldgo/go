// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

/*
#cgo nonblocking nonblockingAdd
#cgo nonblocking nonblockingCallsBack

extern int nonblockingAdd(int, int);
extern void nonblockingCallsBack(void);
*/
import "C"

import (
	"fmt"
	"runtime"
	"sync"
)

func init() {
	register("CgoNonblocking", CgoNonblocking)
	register("CgoNonblockingCallback", CgoNonblockingCallback)
}

// CgoNonblocking makes nonblocking calls from several goroutines while
// collections, which stop the world and scan stacks, run.
func CgoNonblocking() {
	stop := make(chan struct{})
	var gcs sync.WaitGroup
	gcs.Go(func() {
		for {
			select {
			case <-stop:
				return
			default:
				runtime.GC()
			}
		}
	})
	var callers sync.WaitGroup
	for g := 0; g < 4; g++ {
		callers.Go(func() {
			sink := make([][]byte, 16)
			for i := 0; i < 200000; i++ {
				if r := C.nonblockingAdd(C.int(i), C.int(g)); int(r) != i+g {
					panic(fmt.Sprintf("nonblockingAdd(%d, %d) = %d", i, g, r))
				}
				sink[i%len(sink)] = make([]byte, 64)
			}
		})
	}
	callers.Wait()
	close(stop)
	gcs.Wait()
	fmt.Println("OK")
}

//export nonblockingCallback
func nonblockingCallback() {
}

func CgoNonblockingCallback() {
	C.nonblockingCallsBack()
	fmt.Println("OK")
}
