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
	"strings"
	"testing"

	"github.com/hajimehoshi/ebiten/v2/internal/shaderir/spirv"
)

// validate runs spirv-val (Vulkan 1.3 rules) on a module, where SPIRV-Tools
// is installed, and logs spirv-dis's reading of it.
func validate(t *testing.T, b []byte) {
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
	if dis, err := exec.LookPath("spirv-dis"); err == nil {
		out, _ := exec.Command(dis, f).CombinedOutput()
		t.Logf("spirv-dis:\n%s", strings.TrimSpace(string(out)))
	}
}

// MinimalFragment is the smallest valid fragment shader: an entry point that
// returns.
func minimalFragment() *spirv.Module {
	m := spirv.NewModule()
	m.Capability(spirv.CapabilityShader)
	m.MemoryModel(spirv.AddressingLogical, spirv.MemoryModelGLSL450)
	void := m.TypeVoid()
	fnType := m.TypeFunction(void)
	main := m.Function(void, fnType, func(f *spirv.Function) {
		f.Label()
		f.Return()
	})
	m.Name(main, "main")
	m.EntryPoint(spirv.ExecutionModelFragment, main, "main")
	m.ExecutionMode(main, spirv.ExecutionModeOriginUpperLeft)
	return m
}

func TestMinimalFragment(t *testing.T) {
	m := minimalFragment()
	b := m.Bytes()
	if len(b)%4 != 0 || b[0] != 0x03 || b[3] != 0x07 {
		t.Fatalf("not a SPIR-V binary: % x", b[:8])
	}
	validate(t, b)
}

// Types are written once: asking again gives the same ID.
func TestTypesOnce(t *testing.T) {
	m := spirv.NewModule()
	if a, b := m.TypeVoid(), m.TypeVoid(); a != b {
		t.Errorf("void twice: %d, %d", a, b)
	}
	v := m.TypeVoid()
	if a, b := m.TypeFunction(v), m.TypeFunction(v); a != b {
		t.Errorf("void() twice: %d, %d", a, b)
	}
}

// A broken module is caught: an entry point naming no function.
func TestValidationCatchesErrors(t *testing.T) {
	m := spirv.NewModule()
	m.Capability(spirv.CapabilityShader)
	m.MemoryModel(spirv.AddressingLogical, spirv.MemoryModelGLSL450)
	m.EntryPoint(spirv.ExecutionModelFragment, 42, "main")
	val, err := exec.LookPath("spirv-val")
	if err != nil {
		t.Skip("spirv-val is not installed")
	}
	f := filepath.Join(t.TempDir(), "bad.spv")
	os.WriteFile(f, m.Bytes(), 0o644)
	if out, err := exec.Command(val, "--target-env", "vulkan1.3", f).CombinedOutput(); err == nil {
		t.Errorf("spirv-val accepted a broken module:\n%s", out)
	}
}

// ConstantColorFragment writes one colour to the colour attachment at
// Location 0.
func constantColorFragment(rgba [4]float32) *spirv.Module {
	m := spirv.NewModule()
	m.Capability(spirv.CapabilityShader)
	m.MemoryModel(spirv.AddressingLogical, spirv.MemoryModelGLSL450)
	f32 := m.TypeFloat(32)
	vec4 := m.TypeVector(f32, 4)
	out := m.Variable(m.TypePointer(spirv.StorageClassOutput, vec4), spirv.StorageClassOutput)
	m.Decorate(out, spirv.DecorationLocation, 0)
	m.Name(out, "fragColor")
	colour := m.ConstantComposite(vec4, m.ConstantFloat32(rgba[0]), m.ConstantFloat32(rgba[1]), m.ConstantFloat32(rgba[2]), m.ConstantFloat32(rgba[3]))
	void := m.TypeVoid()
	main := m.Function(void, m.TypeFunction(void), func(f *spirv.Function) {
		f.Label()
		f.Store(out, colour)
		f.Return()
	})
	m.Name(main, "main")
	m.EntryPoint(spirv.ExecutionModelFragment, main, "main", out)
	m.ExecutionMode(main, spirv.ExecutionModeOriginUpperLeft)
	return m
}

func TestConstantColorFragment(t *testing.T) {
	validate(t, constantColorFragment([4]float32{1, 0.5, 0, 1}).Bytes())
}

// A vertex shader writing gl_Position (BuiltIn Position) from a constant.
func TestPositionVertex(t *testing.T) {
	m := spirv.NewModule()
	m.Capability(spirv.CapabilityShader)
	m.MemoryModel(spirv.AddressingLogical, spirv.MemoryModelGLSL450)
	f32 := m.TypeFloat(32)
	vec4 := m.TypeVector(f32, 4)
	pos := m.Variable(m.TypePointer(spirv.StorageClassOutput, vec4), spirv.StorageClassOutput)
	m.Decorate(pos, spirv.DecorationBuiltIn, spirv.BuiltInPosition)
	zero, one := m.ConstantFloat32(0), m.ConstantFloat32(1)
	void := m.TypeVoid()
	main := m.Function(void, m.TypeFunction(void), func(f *spirv.Function) {
		f.Label()
		f.Store(pos, m.ConstantComposite(vec4, zero, zero, zero, one))
		f.Return()
	})
	m.EntryPoint(spirv.ExecutionModelVertex, main, "main", pos)
	validate(t, m.Bytes())
}

// Constants and types are written once.
func TestConstantsOnce(t *testing.T) {
	m := spirv.NewModule()
	if a, b := m.ConstantFloat32(0.5), m.ConstantFloat32(0.5); a != b {
		t.Errorf("0.5 twice: %d, %d", a, b)
	}
	if a, b := m.ConstantFloat32(0.5), m.ConstantFloat32(0.25); a == b {
		t.Error("0.5 and 0.25 share an ID")
	}
	f := m.TypeFloat(32)
	if a, b := m.TypeVector(f, 4), m.TypeVector(f, 4); a != b {
		t.Errorf("vec4 twice: %d, %d", a, b)
	}
}
