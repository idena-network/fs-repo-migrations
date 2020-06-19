// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package simplifiedchinese

import (
	"github.com/ipfs/fs-repo-migrations/ipfs-10-to-11/_vendor/golang.org/x/text/encoding"
)

// All is a list of all defined encodings in this package.
var All = []encoding.Encoding{GB18030, GBK, HZGB2312}