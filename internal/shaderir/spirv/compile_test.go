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
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hajimehoshi/ebiten/v2/internal/builtinshader"
	"github.com/hajimehoshi/ebiten/v2/internal/graphics"
	"github.com/hajimehoshi/ebiten/v2/internal/shaderir"
	"github.com/hajimehoshi/ebiten/v2/internal/shaderir/spirv"
)

const redKage = "//kage:unit pixels\npackage main\nfunc Fragment(dst vec4, src vec2, color vec4) vec4 { return vec4(1, 0, 0, 1) }\n"

func wordsToBytes(w []uint32) []byte {
	b := make([]byte, 4*len(w))
	for i, x := range w {
		binary.LittleEndian.PutUint32(b[4*i:], x)
	}
	return b
}

// Ebitengine's built-in vertex function (gl_Position = projection *
// vec4(A0, 0, 1); V0..V2 = A1..A3) compiles to a module spirv-val accepts.
func TestCompileVertexBuiltin(t *testing.T) {
	p, err := graphics.CompileShader([]byte(redKage))
	if err != nil {
		t.Fatal(err)
	}
	vs, err := spirv.CompileVertex(p)
	if err != nil {
		t.Fatal(err)
	}
	validate(t, wordsToBytes(vs))
}

// What the backend doesn't know yet is an error naming it.
func TestCompileVertexUnsupported(t *testing.T) {
	p, err := graphics.CompileShader([]byte(redKage))
	if err != nil {
		t.Fatal(err)
	}
	p.VertexFunc.Block.Stmts = append([]shaderir.Stmt{{Type: shaderir.For}}, p.VertexFunc.Block.Stmts...)
	if _, err := spirv.CompileVertex(p); err == nil || !strings.Contains(err.Error(), "not supported yet") {
		t.Errorf("an unknown statement: %v", err)
	}
}

// The red Kage program's fragment function (return vec4(1, 0, 0, 1))
// compiles to a module spirv-val accepts.
func TestCompileFragmentConstant(t *testing.T) {
	p, err := graphics.CompileShader([]byte(redKage))
	if err != nil {
		t.Fatal(err)
	}
	fs, err := spirv.CompileFragment(p)
	if err != nil {
		t.Fatal(err)
	}
	validate(t, wordsToBytes(fs))
}

// Ebitengine's nearest-filter built-in (imageSrc0UnsafeAt → __texelAt)
// compiles as far as sampling, then names it: texture reads come with the
// Ebitengine's nearest-filter built-in (imageSrc0UnsafeAt, __texelAt, then
// times the vertex colour) compiles to a module spirv-val accepts.
func TestCompileNearestBuiltin(t *testing.T) {
	p, err := graphics.CompileShader([]byte("//kage:unit pixels\npackage main\nfunc Fragment(dstPos vec4, src0Pos vec2, color vec4) vec4 {\n\tclr := imageSrc0UnsafeAt(src0Pos)\n\tclr *= color\n\treturn clr\n}\n"))
	if err != nil {
		t.Fatal(err)
	}
	fs, err := spirv.CompileFragment(p)
	if err != nil {
		t.Fatal(err)
	}
	validate(t, wordsToBytes(fs))
}

// Every built-in shader Ebitengine draws images with (nearest and linear;
// unsafe, clamp-to-zero and repeat addressing) compiles, both stages, to
// modules spirv-val accepts.
func TestCompileBuiltinShaders(t *testing.T) {
	for _, filter := range []builtinshader.Filter{builtinshader.FilterNearest, builtinshader.FilterLinear, builtinshader.FilterPixelated} {
		for _, address := range []builtinshader.Address{builtinshader.AddressUnsafe, builtinshader.AddressClampToZero, builtinshader.AddressRepeat} {
			p, err := graphics.CompileShader(builtinshader.ShaderSource(filter, address))
			if err != nil {
				t.Fatal(err)
			}
			vs, err := spirv.CompileVertex(p)
			if err != nil {
				t.Fatalf("filter %d address %d, vertex: %v", filter, address, err)
			}
			fs, err := spirv.CompileFragment(p)
			if err != nil {
				t.Fatalf("filter %d address %d, fragment: %v", filter, address, err)
			}
			validateQuiet(t, wordsToBytes(vs))
			validateQuiet(t, wordsToBytes(fs))
		}
	}
}

// validateQuiet is validate without the disassembly in the log.
func validateQuiet(t *testing.T, b []byte) {
	t.Helper()
	val, err := exec.LookPath("spirv-val")
	if err != nil {
		t.Skip("spirv-val is not installed")
	}
	f := filepath.Join(t.TempDir(), "m.spv")
	if err := os.WriteFile(f, b, 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(val, "--target-env", "vulkan1.3", f).CombinedOutput(); err != nil {
		t.Fatalf("spirv-val: %v\n%s", err, out)
	}
}
