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
	"slices"
	"testing"

	"github.com/hajimehoshi/ebiten/v2/internal/graphics"
	"github.com/hajimehoshi/ebiten/v2/internal/shaderir"
	"github.com/hajimehoshi/ebiten/v2/internal/shaderir/spirv"
)

func arr(t shaderir.BasicType, n int) shaderir.Type {
	return shaderir.Type{Main: shaderir.Array, Sub: []shaderir.Type{{Main: t}}, Length: n}
}

// The std140 rules, type by type (OpenGL 4.6 §7.6.2.2).
func TestStd140Types(t *testing.T) {
	for _, c := range []struct {
		typ         shaderir.Type
		size, align int
	}{
		{shaderir.Type{Main: shaderir.Float}, 4, 4},
		{shaderir.Type{Main: shaderir.Int}, 4, 4},
		{shaderir.Type{Main: shaderir.Vec2}, 8, 8},
		{shaderir.Type{Main: shaderir.Vec3}, 12, 16},
		{shaderir.Type{Main: shaderir.Vec4}, 16, 16},
		{shaderir.Type{Main: shaderir.Mat2}, 32, 16},
		{shaderir.Type{Main: shaderir.Mat3}, 48, 16},
		{shaderir.Type{Main: shaderir.Mat4}, 64, 16},
		{arr(shaderir.Float, 3), 48, 16}, // each float padded to 16
		{arr(shaderir.Vec2, 4), 64, 16},
		{arr(shaderir.Vec3, 2), 32, 16},
		{shaderir.Type{Main: shaderir.Struct, Sub: []shaderir.Type{{Main: shaderir.Float}, {Main: shaderir.Vec3}}}, 32, 16},
	} {
		size, align, err := spirv.Std140(c.typ)
		if err != nil || size != c.size || align != c.align {
			t.Errorf("%s: size %d align %d (%v), want %d / %d", c.typ.String(), size, align, err, c.size, c.align)
		}
	}
	if _, _, err := spirv.Std140(shaderir.Type{Main: shaderir.Texture}); err == nil {
		t.Error("a texture got a std140 layout")
	}
}

// A block packs members at their alignments: float at 0, vec3 at 16 (not 4),
// a float right after the vec3's 12 bytes (28), a vec2 at 32.
func TestStd140Packing(t *testing.T) {
	offs, size, err := spirv.Std140Offsets([]shaderir.Type{{Main: shaderir.Float}, {Main: shaderir.Vec3}, {Main: shaderir.Float}, {Main: shaderir.Vec2}})
	if err != nil || !slices.Equal(offs, []int{0, 16, 28, 32}) || size != 48 {
		t.Errorf("offsets %v size %d (%v)", offs, size, err)
	}
}

// Ebitengine's preserved uniforms (the simplest Kage program has only them):
// dst size vec2, src sizes [4]vec2, dst origin / size vec2, src origins /
// sizes [4]vec2 each, the projection mat4 — the block the driver will fill.
func TestStd140EbitenginePreserved(t *testing.T) {
	p, err := graphics.CompileShader([]byte("//kage:unit pixels\npackage main\nfunc Fragment(dst vec4, src vec2, color vec4) vec4 { return vec4(1, 0, 0, 1) }\n"))
	if err != nil {
		t.Fatal(err)
	}
	offs, size, err := spirv.Std140Offsets(p.Uniforms)
	if err != nil {
		t.Fatal(err)
	}
	// vec2 0; [4]vec2 at 16 (stride 16: 64 bytes); vec2 80; vec2 88; [4]vec2 96; [4]vec2 160; mat4 224
	if want := []int{0, 16, 80, 88, 96, 160, 224}; !slices.Equal(offs, want) || size != 288 {
		t.Errorf("%v: offsets %v size %d, want %v / 288", p.UniformNames, offs, size, want)
	}
}

// The same block glslang lays out (glslangValidator -V on a std140 block of
// these members, offsets read back with spirv-dis): Ebitengine's preserved
// uniforms, then a float, a vec3, a float packed after the vec3's 12 bytes,
// a mat3 and a float[3].
func TestStd140MatchesGlslang(t *testing.T) {
	v2, v2x4 := shaderir.Type{Main: shaderir.Vec2}, arr(shaderir.Vec2, 4)
	block := []shaderir.Type{v2, v2x4, v2, v2, v2x4, v2x4, {Main: shaderir.Mat4},
		{Main: shaderir.Float}, {Main: shaderir.Vec3}, {Main: shaderir.Float}, {Main: shaderir.Mat3}, arr(shaderir.Float, 3)}
	offs, _, err := spirv.Std140Offsets(block)
	if want := []int{0, 16, 80, 88, 96, 160, 224, 288, 304, 316, 320, 368}; err != nil || !slices.Equal(offs, want) {
		t.Errorf("offsets %v (%v), glslang's %v", offs, err, want)
	}
}
