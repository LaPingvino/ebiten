// Copyright 2026 The Ebitengine Authors
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

package spirv_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/hajimehoshi/ebiten/v2/internal/shader"
	"github.com/hajimehoshi/ebiten/v2/internal/shaderir/spirv"
)

// TestCoverage runs the backend over the shader package's test programs
// (testdata/*.go): every function compiled (CompileLibrary) and the stages a
// program has; what compiles must pass spirv-val. It logs what is still
// unsupported, most common first, and fails if fewer compile than before
// (the floor rises as the backend grows).
func TestCoverage(t *testing.T) {
	dir := "../../shader/testdata"
	files, _ := filepath.Glob(filepath.Join(dir, "*.go"))
	reasons := map[string][]string{}
	ok := 0
	for _, f := range files {
		src, _ := os.ReadFile(f)
		p, err := shader.Compile(src, "Vertex", "Fragment", 0)
		if err != nil {
			reasons["(shader.Compile: "+err.Error()+")"] = append(reasons["(shader.Compile)"], filepath.Base(f))
			continue
		}
		words, el := spirv.CompileLibrary(p)
		var ev, ef error
		if p.VertexFunc.Block != nil {
			_, ev = spirv.CompileVertex(p)
		}
		if p.FragmentFunc.Block != nil {
			_, ef = spirv.CompileFragment(p)
		}
		switch {
		case el != nil:
			k := el.Error()
			if i := strings.Index(k, ": spirv"); i >= 0 {
				k = k[i+2:]
			}
			reasons["L: "+k] = append(reasons["L: "+k], filepath.Base(f))
		case ev != nil:
			reasons["V: "+ev.Error()] = append(reasons["V: "+ev.Error()], filepath.Base(f))
		case ef != nil:
			reasons["F: "+ef.Error()] = append(reasons["F: "+ef.Error()], filepath.Base(f))
		default:
			if msg := valMsg(t, wordsToBytes(words)); msg != "" {
				reasons["spirv-val: "+msg] = append(reasons["spirv-val: "+msg], filepath.Base(f))
			} else {
				ok++
			}
		}
	}
	var keys []string
	for k := range reasons {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return len(reasons[keys[i]]) > len(reasons[keys[j]]) })
	t.Logf("%d of %d compile", ok, len(files))
	const floor = 46 // 2026-11-06
	if ok < floor {
		t.Errorf("only %d of the test programs compile, fewer than %d before", ok, floor)
	}
	for _, k := range keys {
		t.Logf("%3d  %s  (%s)", len(reasons[k]), k, strings.Join(reasons[k][:min(3, len(reasons[k]))], " "))
	}
}

func valMsg(t *testing.T, b []byte) string {
	f := filepath.Join(t.TempDir(), "m.spv")
	os.WriteFile(f, b, 0o644)
	out, err := exec.Command("spirv-val", "--target-env", "vulkan1.3", f).CombinedOutput()
	if err == nil {
		return ""
	}
	line := strings.SplitN(strings.TrimSpace(string(out)), "\n", 2)[0]
	if i := strings.Index(line, ": "); i >= 0 && strings.HasPrefix(line, "error") {
		line = line[i+2:]
	}
	if len(line) > 90 {
		line = line[:90]
	}
	return line
}
