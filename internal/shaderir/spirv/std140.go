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

package spirv

import (
	"fmt"

	"github.com/hajimehoshi/ebiten/v2/internal/shaderir"
)

// The uniform block's layout (DESIGN.md: uniforms in std140, one block per
// program): a shader's uniforms become the members of one Block struct, at
// the offsets Std140Offsets gives — the same offsets the driver packs
// Ebitengine's []uint32 into, and the SPIR-V decorates (Offset, ArrayStride,
// MatrixStride 16).

func roundUp(n, to int) int { return (n + to - 1) / to * to }

// Std140 is a type's size and base alignment in bytes under std140 (the
// OpenGL 4.6 / Vulkan rules for uniform blocks): scalars 4; vec2 8; vec3 and
// vec4 16 (a vec3 takes 12 bytes, aligned as a vec4); a matrix is an array of
// its column vectors; an array's element stride is rounded up to 16 (and so
// its alignment); a struct aligns to its largest member, rounded up to 16,
// and its size up to that.
func Std140(t shaderir.Type) (size, align int, err error) {
	switch t.Main {
	case shaderir.Bool, shaderir.Int, shaderir.Float:
		return 4, 4, nil
	case shaderir.Vec2, shaderir.IVec2:
		return 8, 8, nil
	case shaderir.Vec3, shaderir.IVec3:
		return 12, 16, nil
	case shaderir.Vec4, shaderir.IVec4:
		return 16, 16, nil
	case shaderir.Mat2:
		return 2 * 16, 16, nil
	case shaderir.Mat3:
		return 3 * 16, 16, nil
	case shaderir.Mat4:
		return 4 * 16, 16, nil
	case shaderir.Array:
		if len(t.Sub) != 1 {
			return 0, 0, fmt.Errorf("spirv: an array type without its element type")
		}
		stride, err := Std140ArrayStride(t)
		if err != nil {
			return 0, 0, err
		}
		return stride * t.Length, 16, nil
	case shaderir.Struct:
		_, size, align, err := std140Members(t.Sub)
		if err != nil {
			return 0, 0, err
		}
		align = roundUp(align, 16)
		return roundUp(size, align), align, nil
	}
	return 0, 0, fmt.Errorf("spirv: no std140 layout for %s", t.String())
}

// Std140ArrayStride is an array type's element stride under std140: the
// element's size rounded up to its alignment, and that up to 16.
func Std140ArrayStride(t shaderir.Type) (int, error) {
	size, align, err := Std140(t.Sub[0])
	if err != nil {
		return 0, err
	}
	return roundUp(roundUp(size, align), 16), nil
}

// Std140Offsets are the offsets of uniforms laid out one after another in a
// std140 block, and the block's size (rounded up to 16).
func Std140Offsets(uniforms []shaderir.Type) (offsets []int, size int, err error) {
	offsets, size, _, err = std140Members(uniforms)
	return offsets, roundUp(size, 16), err
}

func std140Members(members []shaderir.Type) (offsets []int, size, align int, err error) {
	align = 4
	for _, m := range members {
		s, a, err := Std140(m)
		if err != nil {
			return nil, 0, 0, err
		}
		size = roundUp(size, a)
		offsets = append(offsets, size)
		size += s
		align = max(align, a)
	}
	return offsets, size, align, nil
}
