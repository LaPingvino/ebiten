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
	"go/constant"

	"github.com/hajimehoshi/ebiten/v2/internal/shaderir"
)

// The SPIR-V backend for shaderir (V3), grown a construct at a time; what it
// doesn't know yet is an error naming it, never a guess.
//
// A program's uniforms are one Block (set 0, binding 0) at the std140
// offsets of Std140Offsets — the layout PackStd140 fills. In the vertex
// function local variable i is: an attribute (Input, Location i) for
// i < len(Attributes); the position (BuiltIn Position) at len(Attributes);
// then the varyings (Output, Locations 0…); then the function's own locals.

type value struct {
	id  uint32
	typ shaderir.Type
}

type variable struct {
	ptr     uint32 // the OpVariable
	typ     shaderir.Type
	storage uint32
	input   bool // an attribute: read-only
}

type compiler struct {
	m        *Module
	p        *shaderir.Program
	uniforms uint32 // the uniform block's variable (0: none)
	vars     map[int]variable
	types    map[shaderir.BasicType]uint32
	result   uint32 // the fragment function's output (Location 0): where a returned value goes; 0 in the vertex function
}

func newCompiler(p *shaderir.Program) *compiler {
	m := NewModule()
	m.Capability(CapabilityShader)
	m.MemoryModel(AddressingLogical, MemoryModelGLSL450)
	return &compiler{m: m, p: p, vars: map[int]variable{}, types: map[shaderir.BasicType]uint32{}}
}

// typ is a shaderir type as a SPIR-V type (arrays and structs: not here —
// they need layout decorations; see blockType).
func (c *compiler) typ(t shaderir.Type) (uint32, error) {
	m := c.m
	f32, i32 := m.TypeFloat(32), m.TypeInt(32, true)
	switch t.Main {
	case shaderir.Bool:
		return m.TypeBool(), nil
	case shaderir.Int:
		return i32, nil
	case shaderir.Float:
		return f32, nil
	case shaderir.Vec2, shaderir.Vec3, shaderir.Vec4:
		return m.TypeVector(f32, uint32(t.Main-shaderir.Vec2+2)), nil
	case shaderir.IVec2, shaderir.IVec3, shaderir.IVec4:
		return m.TypeVector(i32, uint32(t.Main-shaderir.IVec2+2)), nil
	case shaderir.Mat2, shaderir.Mat3, shaderir.Mat4:
		n := uint32(t.Main - shaderir.Mat2 + 2)
		return m.TypeMatrix(m.TypeVector(f32, n), n), nil
	}
	return 0, fmt.Errorf("spirv: type %s not supported yet", t.String())
}

// blockType is a uniform's type inside the block: arrays with their std140
// ArrayStride.
func (c *compiler) blockType(t shaderir.Type) (uint32, error) {
	if t.Main != shaderir.Array {
		return c.typ(t)
	}
	elem, err := c.blockType(t.Sub[0])
	if err != nil {
		return 0, err
	}
	stride, err := Std140ArrayStride(t)
	if err != nil {
		return 0, err
	}
	a := c.m.TypeArray(elem, t.Length)
	c.m.Decorate(a, DecorationArrayStride, uint32(stride))
	return a, nil
}

// uniformBlock declares the uniforms as one std140 Block at set 0, binding 0.
func (c *compiler) uniformBlock() error {
	if len(c.p.Uniforms) == 0 {
		return nil
	}
	offsets, _, err := Std140Offsets(c.p.Uniforms)
	if err != nil {
		return err
	}
	members := make([]uint32, len(c.p.Uniforms))
	for i, u := range c.p.Uniforms {
		if members[i], err = c.blockType(u); err != nil {
			return err
		}
	}
	block := c.m.TypeStruct(members...)
	c.m.Decorate(block, DecorationBlock)
	for i, u := range c.p.Uniforms {
		c.m.MemberDecorate(block, uint32(i), DecorationOffset, uint32(offsets[i]))
		if u.Main == shaderir.Mat2 || u.Main == shaderir.Mat3 || u.Main == shaderir.Mat4 {
			c.m.MemberDecorate(block, uint32(i), DecorationColMajor)
			c.m.MemberDecorate(block, uint32(i), DecorationMatrixStride, 16)
		}
	}
	c.uniforms = c.m.Variable(c.m.TypePointer(StorageClassUniform, block), StorageClassUniform)
	c.m.Decorate(c.uniforms, DecorationDescriptorSet, 0)
	c.m.Decorate(c.uniforms, DecorationBinding, 0)
	return nil
}

func (c *compiler) global(t shaderir.Type, storage uint32) (uint32, error) {
	st, err := c.typ(t)
	if err != nil {
		return 0, err
	}
	return c.m.Variable(c.m.TypePointer(storage, st), storage), nil
}

// CompileVertex is the program's vertex function as a SPIR-V module (entry
// point "main").
func CompileVertex(p *shaderir.Program) ([]uint32, error) {
	c := newCompiler(p)
	if err := c.uniformBlock(); err != nil {
		return nil, err
	}
	var interfaces []uint32
	na, nv := len(p.Attributes), len(p.Varyings)
	for i, a := range p.Attributes {
		v, err := c.global(a, StorageClassInput)
		if err != nil {
			return nil, err
		}
		c.m.Decorate(v, DecorationLocation, uint32(i))
		c.vars[i] = variable{ptr: v, typ: a, storage: StorageClassInput, input: true}
		interfaces = append(interfaces, v)
	}
	pos, _ := c.global(shaderir.Type{Main: shaderir.Vec4}, StorageClassOutput)
	c.m.Decorate(pos, DecorationBuiltIn, BuiltInPosition)
	c.vars[na] = variable{ptr: pos, typ: shaderir.Type{Main: shaderir.Vec4}, storage: StorageClassOutput}
	interfaces = append(interfaces, pos)
	for i, t := range p.Varyings {
		v, err := c.global(t, StorageClassOutput)
		if err != nil {
			return nil, err
		}
		c.m.Decorate(v, DecorationLocation, uint32(i))
		c.vars[na+1+i] = variable{ptr: v, typ: t, storage: StorageClassOutput}
		interfaces = append(interfaces, v)
	}
	if c.uniforms != 0 {
		interfaces = append(interfaces, c.uniforms) // SPIR-V 1.4+: every global the entry point uses
	}
	block := p.VertexFunc.Block
	void := c.m.TypeVoid()
	var bodyErr error
	main := c.m.Function(void, c.m.TypeFunction(void), func(f *Function) {
		f.Label()
		for i, t := range block.LocalVars { // the function's own locals: first in the first block
			st, err := c.typ(t)
			if err != nil {
				bodyErr = err
				return
			}
			c.vars[na+1+nv+i] = variable{ptr: f.LocalVariable(c.m.TypePointer(StorageClassFunction, st)), typ: t, storage: StorageClassFunction}
		}
		if bodyErr = c.block(f, block); bodyErr != nil {
			return
		}
	})
	if bodyErr != nil {
		return nil, bodyErr
	}
	c.m.EntryPoint(ExecutionModelVertex, main, "main", interfaces...)
	return c.m.Words(), nil
}

// block writes a block's statements; it ends with a Return if they don't.
func (c *compiler) block(f *Function, b *shaderir.Block) error {
	for _, s := range b.Stmts {
		switch s.Type {
		case shaderir.Assign:
			lhs := s.Exprs[0]
			if lhs.Type != shaderir.LocalVariable {
				return fmt.Errorf("spirv: assignment to expression type %d not supported yet", lhs.Type)
			}
			v, ok := c.vars[lhs.Index]
			if !ok || v.input {
				return fmt.Errorf("spirv: assignment to local %d (an attribute or unknown) not supported yet", lhs.Index)
			}
			rhs, err := c.expr(f, s.Exprs[1])
			if err != nil {
				return err
			}
			f.Store(v.ptr, rhs.id)
		case shaderir.Return:
			if len(s.Exprs) > 0 {
				if c.result == 0 {
					return fmt.Errorf("spirv: a returned value outside the fragment function")
				}
				v, err := c.expr(f, s.Exprs[0])
				if err != nil {
					return err
				}
				if v.typ.Main != shaderir.Vec4 {
					return fmt.Errorf("spirv: the fragment function returns %s, not vec4", v.typ.String())
				}
				f.Store(c.result, v.id)
			}
			f.Return()
			return nil
		default:
			return fmt.Errorf("spirv: statement type %d not supported yet", s.Type)
		}
	}
	f.Return()
	return nil
}

// expr evaluates an expression to a value.
func (c *compiler) expr(f *Function, e shaderir.Expr) (value, error) {
	switch e.Type {
	case shaderir.NumberExpr:
		// A number on its own is a float here (Kage's untyped constants in
		// float contexts); integer contexts come with their constructs.
		v, _ := constant.Float32Val(constant.ToFloat(e.Const))
		return value{c.m.ConstantFloat32(v), shaderir.Type{Main: shaderir.Float}}, nil
	case shaderir.LocalVariable:
		v, ok := c.vars[e.Index]
		if !ok {
			return value{}, fmt.Errorf("spirv: unknown local %d", e.Index)
		}
		st, err := c.typ(v.typ)
		if err != nil {
			return value{}, err
		}
		return value{f.Load(st, v.ptr), v.typ}, nil
	case shaderir.UniformVariable:
		if e.Index >= len(c.p.Uniforms) {
			return value{}, fmt.Errorf("spirv: unknown uniform %d", e.Index)
		}
		t := c.p.Uniforms[e.Index]
		st, err := c.typ(t)
		if err != nil {
			return value{}, err
		}
		ptr := f.AccessChain(c.m.TypePointer(StorageClassUniform, st), c.uniforms, c.m.ConstantInt32(int32(e.Index)))
		return value{f.Load(st, ptr), t}, nil
	case shaderir.Call:
		callee := e.Exprs[0]
		if callee.Type != shaderir.BuiltinFuncExpr {
			return value{}, fmt.Errorf("spirv: calls of expression type %d not supported yet", callee.Type)
		}
		var t shaderir.Type
		switch callee.BuiltinFunc {
		case shaderir.Vec2F:
			t = shaderir.Type{Main: shaderir.Vec2}
		case shaderir.Vec3F:
			t = shaderir.Type{Main: shaderir.Vec3}
		case shaderir.Vec4F:
			t = shaderir.Type{Main: shaderir.Vec4}
		default:
			return value{}, fmt.Errorf("spirv: built-in %s not supported yet", callee.BuiltinFunc)
		}
		var parts []uint32
		n := 0
		for _, a := range e.Exprs[1:] {
			v, err := c.expr(f, a)
			if err != nil {
				return value{}, err
			}
			parts = append(parts, v.id)
			n += max(v.typ.DwordCount(), 1)
		}
		st, _ := c.typ(t)
		if n == 1 { // vecN(x): every component x
			for len(parts) < t.DwordCount() {
				parts = append(parts, parts[0])
			}
		} else if n != t.DwordCount() {
			return value{}, fmt.Errorf("spirv: %s with %d components", callee.BuiltinFunc, n)
		}
		return value{f.CompositeConstruct(st, parts...), t}, nil // vector constituents may be vectors
	case shaderir.Binary:
		l, err := c.expr(f, e.Exprs[0])
		if err != nil {
			return value{}, err
		}
		r, err := c.expr(f, e.Exprs[1])
		if err != nil {
			return value{}, err
		}
		if e.Op == shaderir.MatrixMul && l.typ.Main >= shaderir.Mat2 && l.typ.Main <= shaderir.Mat4 && r.typ.Main >= shaderir.Vec2 && r.typ.Main <= shaderir.Vec4 {
			st, _ := c.typ(r.typ)
			return value{f.MatrixTimesVector(st, l.id, r.id), r.typ}, nil
		}
		return value{}, fmt.Errorf("spirv: operator %d on %s and %s not supported yet", e.Op, l.typ.String(), r.typ.String())
	}
	return value{}, fmt.Errorf("spirv: expression type %d not supported yet", e.Type)
}

// CompileFragment is the program's fragment function as a SPIR-V module
// (entry point "main", OriginUpperLeft). Its local 0 is the destination
// position — gl_FragCoord as it is: Vulkan's is top-down in pixels, like
// Metal's [[position]] (OpenGL's backend flips it only for its window); the
// varyings follow (Input, Locations 0…), then the function's own locals; the
// returned colour goes to Output Location 0.
func CompileFragment(p *shaderir.Program) ([]uint32, error) {
	c := newCompiler(p)
	if err := c.uniformBlock(); err != nil {
		return nil, err
	}
	vec4 := shaderir.Type{Main: shaderir.Vec4}
	pos, _ := c.global(vec4, StorageClassInput)
	c.m.Decorate(pos, DecorationBuiltIn, BuiltInFragCoord)
	c.vars[0] = variable{ptr: pos, typ: vec4, storage: StorageClassInput, input: true}
	interfaces := []uint32{pos}
	nv := len(p.Varyings)
	for i, t := range p.Varyings {
		v, err := c.global(t, StorageClassInput)
		if err != nil {
			return nil, err
		}
		c.m.Decorate(v, DecorationLocation, uint32(i))
		c.vars[1+i] = variable{ptr: v, typ: t, storage: StorageClassInput, input: true}
		interfaces = append(interfaces, v)
	}
	c.result, _ = c.global(vec4, StorageClassOutput)
	c.m.Decorate(c.result, DecorationLocation, 0)
	interfaces = append(interfaces, c.result)
	if c.uniforms != 0 {
		interfaces = append(interfaces, c.uniforms)
	}
	block := p.FragmentFunc.Block
	void := c.m.TypeVoid()
	var bodyErr error
	main := c.m.Function(void, c.m.TypeFunction(void), func(f *Function) {
		f.Label()
		for i, t := range block.LocalVars {
			st, err := c.typ(t)
			if err != nil {
				bodyErr = err
				return
			}
			c.vars[1+nv+i] = variable{ptr: f.LocalVariable(c.m.TypePointer(StorageClassFunction, st)), typ: t, storage: StorageClassFunction}
		}
		bodyErr = c.block(f, block)
	})
	if bodyErr != nil {
		return nil, bodyErr
	}
	c.m.EntryPoint(ExecutionModelFragment, main, "main", interfaces...)
	c.m.ExecutionMode(main, ExecutionModeOriginUpperLeft)
	return c.m.Words(), nil
}
