// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

#include "_cgo_export.h"

int nonblockingAdd(int x, int y) {
	return x + y;
}

void nonblockingCallsBack(void) {
	nonblockingCallback();
}
