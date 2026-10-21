// Copyright 2026 NVIDIA CORPORATION
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package tool

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/bazelbuild/reclient/internal/pkg/execroot"
	"github.com/bazelbuild/reclient/internal/pkg/inputprocessor"
	"github.com/bazelbuild/remote-apis-sdks/go/pkg/command"
	"github.com/google/go-cmp/cmp"
)

func TestTableGenPreprocessor(t *testing.T) {
	for _, sharder := range []bool{false, true} {
		name, input := "mlir-tblgen", "src/TestOps.td"
		if sharder {
			name, input = "mlir-src-sharder", "src/TestOps.cpp"
		}
		t.Run(name, func(t *testing.T) {
			er, cleanup := execroot.Setup(t, nil)
			defer cleanup()
			execroot.AddFilesWithContent(t, er, map[string][]byte{
				"bin/" + name: nil, input: nil,
				"include with spaces/nested/Included.td":               nil,
				"include with spaces/.reproxy_tmp/action/Unrelated.td": nil,
				"include with spaces/Ignored.cpp":                      nil,
			})
			args := []string{filepath.Join(er, "bin", name), "-I" + filepath.Join(er, "include with spaces"), "-I", "../include with spaces", filepath.Join(er, input), "-o", "TestOps.inc", "-d", "TestOps.inc.d"}
			outputs := []string{"wd/TestOps.inc", "wd/TestOps.inc.d"}
			inputs := []string{"bin/" + name, input}
			if sharder {
				args = append(args, "-op-shard-index=0")
			} else {
				args = append(args, "-gen-register-info")
				inputs = append(inputs, "include with spaces/nested/Included.td")
				for _, suffix := range []string{"Enums", "Header", "MCDesc", "TargetDesc"} {
					outputs = append(outputs, "wd/TestOps"+suffix+".inc")
				}
			}
			pp := &Preprocessor{&inputprocessor.BasePreprocessor{Ctx: context.Background()}}
			got, err := inputprocessor.Compute(pp, inputprocessor.Options{ExecRoot: er, WorkingDir: "wd", Cmd: args})
			if err != nil {
				t.Fatal(err)
			}
			want := &inputprocessor.ActionSpec{InputSpec: &command.InputSpec{Inputs: inputs}, OutputFiles: outputs, EmittedDependencyFile: "wd/TestOps.inc.d"}
			if diff := cmp.Diff(want, got, strSliceCmp); diff != "" {
				t.Fatalf("(-want +got): %s", diff)
			}
			if sharder {
				return
			}
			// A new include must be discovered without restarting the processor.
			execroot.AddFilesWithContent(t, er, map[string][]byte{"include with spaces/Added.td": nil})
			got, err = inputprocessor.Compute(pp, inputprocessor.Options{ExecRoot: er, WorkingDir: "wd", Cmd: args})
			if err != nil {
				t.Fatal(err)
			}
			want.InputSpec.Inputs = append(want.InputSpec.Inputs, "include with spaces/Added.td")
			if diff := cmp.Diff(want, got, strSliceCmp); diff != "" {
				t.Fatalf("new include (-want +got): %s", diff)
			}
		})
	}
}

func TestTableGenMalformedFlags(t *testing.T) {
	for _, args := range [][]string{
		{"mlir-tblgen", "input.td", "-o"},
		{"mlir-tblgen", "-o", "out.inc"},
		{"mlir-tblgen", "input.td", "-d", "out.inc.d"},
		{"mlir-tblgen", "input.td", "second.td", "-o", "out.inc"},
		{"mlir-tblgen", "input.td", "-o", "out.inc", "-I"},
	} {
		if _, err := parseTableGenFlags(args, "wd", "/root"); err == nil {
			t.Errorf("expected error for %v", args)
		}
	}
}

func TestTableGenDepfileBeforeOutput(t *testing.T) {
	got, err := parseTableGenFlags([]string{"llvm-tblgen", "input.td", "-d", "out.d", "-gen-register-info", "-o", "subdir/out.inc"}, "wd", "/root")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"out.d", "subdir/out.inc", "subdir/outEnums.inc", "subdir/outHeader.inc", "subdir/outMCDesc.inc", "subdir/outTargetDesc.inc"}
	if diff := cmp.Diff(want, got.OutputFilePaths, strSliceCmp); diff != "" {
		t.Fatal(diff)
	}
}

func TestTableGenNonPathOptionValues(t *testing.T) {
	args := []string{"mlir-tblgen", "--prefix", "Conversion", "-name", "Conversion", "-gen-pass-capi-header", "input.td", "-o", "out.inc"}
	got, err := parseTableGenFlags(args, "wd", "/root")
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff([]string{"input.td"}, got.TargetFilePaths); diff != "" {
		t.Fatal(diff)
	}
}
