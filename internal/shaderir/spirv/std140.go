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

// PackStd140 appends Ebitengine's uniforms — packed tightly, each taking its
// type's DwordCount, matrices column-major — to dst in the std140 block
// layout of Std140Offsets (zero padding between members, matrix columns and
// array elements at 16-byte strides), the block's whole size. The uniform at
// flipY (−1 none) has its second row negated, as Metal and DirectX flip the
// projection — the Vulkan driver passes −1: its NDC y points down like its
// framebuffer, so Ebitengine's projection needs no flip (DESIGN.md, "Y axis").
func PackStd140(dst []uint32, types []shaderir.Type, uniforms []uint32, flipY int) ([]uint32, error) {
	offsets, size, err := Std140Offsets(types)
	if err != nil {
		return nil, err
	}
	base := len(dst)
	dst = append(dst, make([]uint32, size/4)...)
	block := dst[base:]
	idx := 0
	for i, t := range types {
		n := t.DwordCount()
		if n < 0 || idx+n > len(uniforms) {
			return nil, fmt.Errorf("spirv: uniform %d (%s): %d dwords given, %d needed", i, t.String(), len(uniforms)-idx, n)
		}
		if err := packOne(block[offsets[i]/4:], t, uniforms[idx:idx+n]); err != nil {
			return nil, err
		}
		if i == flipY && t.Main == shaderir.Mat4 {
			for col := 0; col < 4; col++ {
				block[offsets[i]/4+col*4+1] ^= 1 << 31 // the float's sign: row 1 of each column
			}
		}
		idx += n
	}
	return dst, nil
}

// packOne writes one value of type t (its tightly packed dwords src) at the
// start of dst in std140.
func packOne(dst []uint32, t shaderir.Type, src []uint32) error {
	switch t.Main {
	case shaderir.Bool, shaderir.Int, shaderir.Float, shaderir.Vec2, shaderir.Vec3, shaderir.Vec4,
		shaderir.IVec2, shaderir.IVec3, shaderir.IVec4, shaderir.Mat4:
		copy(dst, src)
	case shaderir.Mat2, shaderir.Mat3: // columns at a 16-byte (4-dword) stride
		n := 2
		if t.Main == shaderir.Mat3 {
			n = 3
		}
		for c := 0; c < n; c++ {
			copy(dst[c*4:c*4+n], src[c*n:c*n+n])
		}
	case shaderir.Array:
		stride, err := Std140ArrayStride(t)
		if err != nil {
			return err
		}
		en := t.Sub[0].DwordCount()
		for e := 0; e < t.Length; e++ {
			if err := packOne(dst[e*stride/4:], t.Sub[0], src[e*en:e*en+en]); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("spirv: no std140 packing for %s", t.String())
	}
	return nil
}
