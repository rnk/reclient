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
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/bazelbuild/reclient/internal/pkg/inputprocessor"
	"github.com/bazelbuild/reclient/internal/pkg/inputprocessor/flags"
	"github.com/bazelbuild/reclient/internal/pkg/pathtranslator"
	"github.com/bazelbuild/remote-apis-sdks/go/pkg/command"
)

// TableGen tools share an input/output convention, including MLIR's source
// sharder, which copies a .cpp input without processing its C++ includes.
func isTableGen(executable string) bool {
	switch strings.TrimSuffix(filepath.Base(executable), ".exe") {
	case "llvm-tblgen", "llvm-min-tblgen", "clang-tblgen", "mlir-tblgen", "mlir-src-sharder":
		return true
	}
	return false
}

func parseTableGenFlags(args []string, workingDir, execRoot string) (*flags.CommandFlags, error) {
	res := &flags.CommandFlags{ExecutablePath: args[0], WorkingDirectory: workingDir, ExecRoot: execRoot}
	registerInfo := false
	output := ""
	for i := 1; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-I" || arg == "-o" || arg == "-d":
			if i+1 == len(args) || args[i+1] == "" {
				return nil, fmt.Errorf("%s requires a path", arg)
			}
			i++
			switch arg {
			case "-I":
				res.IncludeDirPaths = append(res.IncludeDirPaths, args[i])
			case "-o":
				output = args[i]
				res.OutputFilePaths = append(res.OutputFilePaths, args[i])
			case "-d":
				res.EmittedDependencyFile = args[i]
				res.OutputFilePaths = append(res.OutputFilePaths, args[i])
			}
		case strings.HasPrefix(arg, "-I"):
			res.IncludeDirPaths = append(res.IncludeDirPaths, strings.TrimPrefix(strings.TrimPrefix(arg, "-I"), "="))
		case arg == "-gen-register-info":
			registerInfo = true
		// Non-path option values such as --prefix Conversion and -name
		// Conversion are not inputs. LLVM/MLIR inputs end in .td or .cpp.
		case !strings.HasPrefix(arg, "-") && (filepath.Ext(arg) == ".td" || filepath.Ext(arg) == ".cpp"):
			res.TargetFilePaths = append(res.TargetFilePaths, arg)
		}
	}
	if len(res.TargetFilePaths) != 1 || output == "" {
		return nil, fmt.Errorf("TableGen requires one input and an output: %v", args)
	}
	if registerInfo {
		stem := strings.TrimSuffix(output, filepath.Ext(output))
		for _, suffix := range []string{"Enums", "Header", "MCDesc", "TargetDesc"} {
			res.OutputFilePaths = append(res.OutputFilePaths, stem+suffix+".inc")
		}
	}
	return res, nil
}

// ComputeSpec conservatively uploads the .td closure of the include directories.
// Unlike a cached directory listing, walking for each action notices new files
// during the lifetime of reproxy. The sharder needs only its explicit .cpp input.
func (p *Preprocessor) ComputeSpec() error {
	if !isTableGen(p.Flags.ExecutablePath) || strings.TrimSuffix(filepath.Base(p.Flags.ExecutablePath), ".exe") == "mlir-src-sharder" {
		return nil
	}
	dirs := append([]string{filepath.Dir(p.Flags.TargetFilePaths[0])}, p.Flags.IncludeDirPaths...)
	seen := make(map[string]bool)
	inputs := []string{}
	for _, dir := range dirs {
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(p.Options.ExecRoot, p.Options.WorkingDir, dir)
		}
		dir = filepath.Clean(dir)
		if seen[dir] {
			continue
		}
		seen[dir] = true
		// Like TableGen's include search, allow unused include directories that
		// have not been generated yet. Other filesystem errors remain fatal.
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			continue
		}
		err := walkTableGenInputs(dir, make(map[string]bool), &inputs)
		if err != nil {
			p.Err = fmt.Errorf("discovering TableGen inputs in %q: %w", dir, err)
			return p.Err
		}
	}
	p.AppendSpec(&inputprocessor.ActionSpec{InputSpec: &command.InputSpec{
		Inputs: pathtranslator.ListRelToExecRoot(p.Options.ExecRoot, p.Options.WorkingDir, inputs),
	}})
	return nil
}

// Follow directory symlinks while retaining the paths used by the include
// search. Track ancestors rather than all visited directories: two aliases of
// the same directory must both be available in the remote input tree.
func walkTableGenInputs(dir string, ancestors map[string]bool, inputs *[]string) error {
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return err
	}
	if ancestors[real] {
		return nil
	}
	ancestors[real] = true
	defer delete(ancestors, real)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		path := filepath.Join(dir, entry.Name())
		isDir := entry.IsDir()
		if entry.Type()&os.ModeSymlink != 0 {
			info, err := os.Stat(path)
			if err != nil {
				return err
			}
			isDir = info.IsDir()
		}
		if isDir {
			// The SDK stages outputs here; another action can remove them.
			if entry.Name() == ".reproxy_tmp" {
				continue
			}
			if err := walkTableGenInputs(path, ancestors, inputs); err != nil {
				return err
			}
		} else if filepath.Ext(path) == ".td" {
			*inputs = append(*inputs, path)
		}
	}
	return nil
}
