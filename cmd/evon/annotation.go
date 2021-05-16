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
	"fmt"
	"go/ast"
	"go/token"
	"regexp"
	"sort"
	"strings"
)

type annotation struct {
	Pos   token.Pos
	Flags map[string]bool
}

func (ann *annotation) FormatFlags() string {
	res := []string{}
	for f := range ann.Flags {
		res = append(res, f)
	}
	sort.Strings(res)
	return "(" + strings.Join(res, ", ") + ")"
}

var annRe = regexp.MustCompile(`@evon\(\s*(.*?)\s*\)`)

const (
	annCatch = "catch"
	annLock  = "lock"
	annPause = "pause"
	annQueue = "queue"
	annSpawn = "spawn"
	annUnusb = "unsub"
	annWait  = "wait"

	annSep = ","
)

var validFlags = map[string]bool{
	annCatch: true,
	annLock:  true,
	annPause: true,
	annQueue: true,
	annSpawn: true,
	annUnusb: true,
	annWait:  true,
}

func extractAnnotation(cg *ast.CommentGroup, fset *token.FileSet) (*annotation, error) {
	ann := &annotation{Flags: map[string]bool{}}

	foundCmt := (*ast.Comment)(nil)
	foundOffsets := []int(nil)

	for _, cmt := range cg.List {
		matches := annRe.FindAllStringSubmatchIndex(cmt.Text, 2)

		if matches == nil {
			continue
		}

		if foundCmt != nil {
			nextPos := fset.Position(cmt.Pos() + token.Pos(matches[0][0]))
			nextPos.Filename = ""
			return nil, fmt.Errorf("%s: Redundant annotation at %s", fset.Position(ann.Pos), nextPos)
		}

		foundCmt = cmt
		foundOffsets = matches[0]
		ann.Pos = foundCmt.Pos() + token.Pos(foundOffsets[0])

		if len(matches) > 1 {
			nextPos := fset.Position(cmt.Pos() + token.Pos(matches[1][0]))
			nextPos.Filename = ""
			return nil, fmt.Errorf("%s: Redundant annotation at %s", fset.Position(ann.Pos), nextPos)
		}
	}

	if foundCmt == nil {
		return nil, nil
	}

	for _, flag := range strings.Split(foundCmt.Text[foundOffsets[2]:foundOffsets[3]], annSep) {
		flag = strings.TrimSpace(flag)

		if len(flag) == 0 {
			continue
		}

		if !validFlags[flag] {
			return nil, fmt.Errorf(`%s: Invalid flag "%s"`, fset.Position(ann.Pos), flag)
		}

		ann.Flags[flag] = true
	}

	if ann.Flags[annSpawn] && ann.Flags[annQueue] {
		return nil, fmt.Errorf(`%s: Flag "%s" cannot coexist with "%s"`,
			fset.Position(ann.Pos), annSpawn, annQueue)
	}

	if ann.Flags[annWait] && !(ann.Flags[annSpawn] || ann.Flags[annQueue]) {
		return nil, fmt.Errorf(`%s: Flag "%s" can only be used together with "%s" or "%s"`,
			fset.Position(ann.Pos), annWait, annSpawn, annQueue)
	}

	return ann, nil
}
