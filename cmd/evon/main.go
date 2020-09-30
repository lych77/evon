// Copyright (c) 2020, lych77
// All rights reserved.
//
// Redistribution and use in source and binary forms, with or without
// modification, are permitted provided that the following conditions are met:
//
// 1. Redistributions of source code must retain the above copyright notice, this
//    list of conditions and the following disclaimer.
//
// 2. Redistributions in binary form must reproduce the above copyright notice,
//    this list of conditions and the following disclaimer in the documentation
//    and/or other materials provided with the distribution.
//
// 3. Neither the name of the copyright holder nor the names of its
//    contributors may be used to endorse or promote products derived from
//    this software without specific prior written permission.
//
// THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS "AS IS"
// AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE
// IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE ARE
// DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT HOLDER OR CONTRIBUTORS BE LIABLE
// FOR ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL, EXEMPLARY, OR CONSEQUENTIAL
// DAMAGES (INCLUDING, BUT NOT LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR
// SERVICES; LOSS OF USE, DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER
// CAUSED AND ON ANY THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY,
// OR TORT (INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE
// OF THIS SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.

package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/tools/go/packages"
)

var (
	flagHandlerSuffix = flag.String("handler_suffix", "Handler", "Required suffix of the event handler type names")
	flagEventSuffix   = flag.String("event_suffix", "Event", "Suffix of the generated event type names")
	flagOut           = flag.String("out", "evon_gen.go", `Output source file name`)
	flagShow          = flag.Bool("show", false, "Show event handler types without generation")
)

func main() {
	flag.Parse()

	wd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Fatal: %s\n", err)
		return
	}

	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedImports | packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo | packages.NeedDeps,
	}

	pkgs, err := packages.Load(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Fatal: %s\n", err)
		return
	}

	process(pkgs[0], filepath.Join(wd, *flagOut))
}
