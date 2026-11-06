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
	result   uint32        // the fragment function's output (Location 0): where a returned value goes; 0 in the vertex function
	inFunc   bool          // compiling a user function: a returned value is OpReturnValue
	retType  shaderir.Type // the user function's return type (an untyped number returned takes it)
	funcs    map[int]uint32
	// blockVars are each nested block's own variables, by local index:
	// indices are reused across sibling scopes, so a block's are mapped in
	// only while its statements are compiled (stmts).
	blockVars map[*shaderir.Block]map[int]variable
	loops     [][2]uint32    // the enclosing loops' continue and merge labels (innermost last): continue / break
	textures  map[int]uint32 // texture i's combined image sampler (set 0, binding 1 + i), made on first use
	texList   []uint32       // the same, in order of use: for the entry point's interface (with frontFacing)
	// frontFacing is the fragment stage's gl_FrontFacing (Input, BuiltIn
	// FrontFacing), made on first use of frontfacing().
	frontFacing uint32
	// zeroes are each block's own variables (pointer, type), set to zero
	// each time the block is entered — Kage's (Go's) declarations zero
	// them, a loop body's on every iteration; the variables themselves are
	// all declared at the function's start.
	zeroes map[*shaderir.Block][][2]uint32
}

func newCompiler(p *shaderir.Program) *compiler {
	m := NewModule()
	m.Capability(CapabilityShader)
	m.MemoryModel(AddressingLogical, MemoryModelGLSL450)
	return &compiler{m: m, p: p, vars: map[int]variable{}, types: map[shaderir.BasicType]uint32{}, funcs: map[int]uint32{}, textures: map[int]uint32{}}
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
	case shaderir.Array: // a local array (the uniform block's have their strides: blockType)
		if len(t.Sub) != 1 {
			return 0, fmt.Errorf("spirv: an array type without its element type")
		}
		elem, err := c.typ(t.Sub[0])
		if err != nil {
			return 0, err
		}
		return m.TypeArrayPlain(elem, t.Length), nil
	}
	return 0, fmt.Errorf("spirv: type %s not supported yet", t.String())
}

// blockType is a uniform's type inside the block: arrays with their std140
// ArrayStride.
func (c *compiler) blockType(t shaderir.Type) (uint32, error) {
	if t.Main == shaderir.Bool { // a block can't hold a bool: an int (Ebitengine packs it as one dword), read as != 0
		return c.m.TypeInt(32, true), nil
	}
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
		isMat := func(t shaderir.Type) bool {
			return t.Main == shaderir.Mat2 || t.Main == shaderir.Mat3 || t.Main == shaderir.Mat4
		}
		if isMat(u) || u.Main == shaderir.Array && len(u.Sub) == 1 && isMat(u.Sub[0]) { // a matrix, or an array of them: the member carries the layout
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
	if block == nil {
		return nil, fmt.Errorf("spirv: the program has no vertex function")
	}
	if err := c.userFuncs(block); err != nil {
		return nil, err
	}
	void := c.m.TypeVoid()
	var bodyErr error
	main := c.m.Function(void, c.m.TypeFunction(void), func(f *Function) {
		f.Label()
		for i, t := range block.LocalVars { // the function's own locals: first in the first block
			if t.Main == shaderir.None { // a for loop's counter: the for declares it, with its type
				continue
			}
			st, err := c.typ(t)
			if err != nil {
				bodyErr = err
				return
			}
			ptr := f.LocalVariable(c.m.TypePointer(StorageClassFunction, st))
			c.zeroOnEntry(block, ptr, st)
			c.vars[na+1+nv+i] = variable{ptr: ptr, typ: t, storage: StorageClassFunction}
		}
		if bodyErr = c.declareNested(f, block); bodyErr != nil {
			return
		}
		if bodyErr = c.copyAssignedInputs(f, block); bodyErr != nil {
			return
		}
		if bodyErr = c.block(f, block); bodyErr != nil {
			return
		}
	})
	if bodyErr != nil {
		return nil, bodyErr
	}
	c.m.EntryPoint(ExecutionModelVertex, main, "main", append(interfaces, c.texList...)...)
	return c.m.Words(), nil
}

// block writes a block's statements; it ends with a Return if they don't.
func (c *compiler) block(f *Function, b *shaderir.Block) error {
	ended, err := c.stmts(f, b)
	if err != nil {
		return err
	}
	if !ended {
		f.Return()
	}
	return nil
}

// declareNested declares the locals of the blocks nested in b (if / for
// bodies) — every Function variable must be at the start of the function's
// first block.
func (c *compiler) declareNested(f *Function, b *shaderir.Block) error {
	for _, s := range b.Stmts {
		if s.Type == shaderir.For { // the loop's counter: declared by the for itself
			if _, ok := c.vars[s.ForVarIndex]; !ok {
				st, err := c.typ(s.ForVarType)
				if err != nil {
					return err
				}
				c.vars[s.ForVarIndex] = variable{ptr: f.LocalVariable(c.m.TypePointer(StorageClassFunction, st)), typ: s.ForVarType, storage: StorageClassFunction}
			}
		}
		for _, bb := range s.Blocks {
			if bb == nil {
				continue
			}
			for i, t := range bb.LocalVars {
				if t.Main == shaderir.None { // a for loop's counter: the for declares it, with its type
					continue
				}
				st, err := c.typ(t)
				if err != nil {
					return err
				}
				if c.blockVars == nil {
					c.blockVars = map[*shaderir.Block]map[int]variable{}
				}
				if c.blockVars[bb] == nil {
					c.blockVars[bb] = map[int]variable{}
				}
				ptr := f.LocalVariable(c.m.TypePointer(StorageClassFunction, st))
				c.zeroOnEntry(bb, ptr, st)
				c.blockVars[bb][bb.LocalVarIndexOffset+i] = variable{ptr: ptr, typ: t, storage: StorageClassFunction}
			}
			if err := c.declareNested(f, bb); err != nil {
				return err
			}
		}
	}
	return nil
}

// stmts writes a block's statements; ended reports that the block was
// closed (by a return), so nothing may follow in it.
func (c *compiler) stmts(f *Function, b *shaderir.Block) (ended bool, err error) {
	for _, z := range c.zeroes[b] { // the block's declarations: zero on entry
		f.Store(z[0], c.m.ConstantNull(z[1]))
	}
	if own := c.blockVars[b]; len(own) > 0 { // this block's variables over the outer ones of the same index
		saved := map[int]variable{}
		absent := map[int]bool{}
		for i, v := range own {
			if old, ok := c.vars[i]; ok {
				saved[i] = old
			} else {
				absent[i] = true
			}
			c.vars[i] = v
		}
		defer func() {
			for i := range own {
				if absent[i] {
					delete(c.vars, i)
				} else {
					c.vars[i] = saved[i]
				}
			}
		}()
	}
	for _, s := range b.Stmts {
		switch s.Type {
		case shaderir.Assign:
			lhs := s.Exprs[0]
			if lhs.Type == shaderir.FieldSelector {
				if err := c.assignField(f, lhs, s.Exprs[1]); err != nil {
					return false, err
				}
				continue
			}
			if lhs.Type == shaderir.Index {
				if err := c.assignIndex(f, lhs, s.Exprs[1]); err != nil {
					return false, err
				}
				continue
			}
			if lhs.Type != shaderir.LocalVariable {
				return false, fmt.Errorf("spirv: assignment to expression type %d not supported yet", lhs.Type)
			}
			v, ok := c.vars[lhs.Index]
			if !ok || v.input {
				return false, fmt.Errorf("spirv: assignment to local %d (an attribute or unknown) not supported yet", lhs.Index)
			}
			var rhs value
			var err error
			if s.Exprs[1].Type == shaderir.NumberExpr && (v.typ.Main == shaderir.Int || v.typ.Main == shaderir.Float) {
				var id uint32 // an untyped number takes the variable's type (n := 0)
				id, err = c.forConst(v.typ, s.Exprs[1].Const)
				rhs = value{id, v.typ}
			} else {
				rhs, err = c.expr(f, s.Exprs[1])
			}
			if err != nil {
				return false, err
			}
			f.Store(v.ptr, rhs.id)
		case shaderir.Return:
			if len(s.Exprs) > 0 {
				var v value
				var err error
				if c.inFunc && s.Exprs[0].Type == shaderir.NumberExpr && (c.retType.Main == shaderir.Int || c.retType.Main == shaderir.Float) {
					var id uint32 // an untyped number takes the function's return type
					id, err = c.forConst(c.retType, s.Exprs[0].Const)
					v = value{id, c.retType}
				} else {
					v, err = c.expr(f, s.Exprs[0])
				}
				if err != nil {
					return false, err
				}
				if c.inFunc {
					f.ReturnValue(v.id)
					return true, nil
				}
				if c.result == 0 {
					return false, fmt.Errorf("spirv: a returned value outside the fragment function")
				}
				if v.typ.Main != shaderir.Vec4 {
					return false, fmt.Errorf("spirv: the fragment function returns %s, not vec4", v.typ.String())
				}
				f.Store(c.result, v.id)
			}
			f.Return()
			return true, nil
		case shaderir.Discard: // discard: OpKill (core in Vulkan 1.3's SPIR-V; it ends the block)
			f.Kill()
			return true, nil
		case shaderir.ExprStmt: // a call for its effects; the value dropped
			if _, err := c.expr(f, s.Exprs[0]); err != nil {
				return false, err
			}
		case shaderir.Init: // var x T: the zero value
			v, ok := c.vars[s.InitIndex]
			if !ok {
				return false, fmt.Errorf("spirv: init of an unknown local %d", s.InitIndex)
			}
			st, err := c.typ(v.typ)
			if err != nil {
				return false, err
			}
			f.Store(v.ptr, c.m.ConstantNull(st))
		case shaderir.BlockStmt:
			if ended, err := c.stmts(f, s.Blocks[0]); err != nil || ended {
				return ended, err
			}
		case shaderir.If:
			if ended, err := c.ifStmt(f, s); err != nil || ended {
				return ended, err
			}
		case shaderir.For:
			if err := c.forStmt(f, s); err != nil {
				return false, err
			}
		case shaderir.Continue, shaderir.Break:
			if len(c.loops) == 0 {
				return false, fmt.Errorf("spirv: continue / break outside a loop")
			}
			l := c.loops[len(c.loops)-1]
			if s.Type == shaderir.Continue {
				f.Branch(l[0])
			} else {
				f.Branch(l[1])
			}
			return true, nil
		default:
			return false, fmt.Errorf("spirv: statement type %d not supported yet", s.Type)
		}
	}
	return false, nil
}

// ifStmt is a structured selection: the condition, OpSelectionMerge, the
// conditional branch; each branch to the merge block unless it returned.
// ended: both branches returned (the merge block is then unreachable and
// closed with OpUnreachable… here a Return keeps it simple and valid).
func (c *compiler) ifStmt(f *Function, s shaderir.Stmt) (bool, error) {
	cond, err := c.expr(f, s.Exprs[0])
	if err != nil {
		return false, err
	}
	if cond.typ.Main != shaderir.Bool {
		return false, fmt.Errorf("spirv: if on a %s", cond.typ.String())
	}
	thenL, mergeL := c.m.ID(), c.m.ID()
	elseL := mergeL
	if len(s.Blocks) > 1 && s.Blocks[1] != nil {
		elseL = c.m.ID()
	}
	f.SelectionMerge(mergeL)
	f.BranchConditional(cond.id, thenL, elseL)
	f.LabelAt(thenL)
	thenEnded, err := c.stmts(f, s.Blocks[0])
	if err != nil {
		return false, err
	}
	if !thenEnded {
		f.Branch(mergeL)
	}
	elseEnded := false
	if elseL != mergeL {
		f.LabelAt(elseL)
		if elseEnded, err = c.stmts(f, s.Blocks[1]); err != nil {
			return false, err
		}
		if !elseEnded {
			f.Branch(mergeL)
		}
	}
	f.LabelAt(mergeL)
	if thenEnded && elseEnded {
		f.Unreachable() // the merge block can't be reached; it still needs an end
		return true, nil
	}
	return false, nil
}

// forConst is a for loop's constant in the counter's type.
func (c *compiler) forConst(t shaderir.Type, v constant.Value) (uint32, error) {
	switch t.Main {
	case shaderir.Int:
		n, _ := constant.Int64Val(constant.ToInt(v))
		return c.m.ConstantInt32(int32(n)), nil
	case shaderir.Float:
		x, _ := constant.Float32Val(constant.ToFloat(v))
		return c.m.ConstantFloat32(x), nil
	}
	return 0, fmt.Errorf("spirv: a for loop over %s", t.String())
}

// forStmt is Kage's for loop (counter := init; counter op end; counter +=
// delta) as a structured loop: the counter set, a header (OpLoopMerge, to the
// condition block), the condition (the body or the merge), the body (to the
// continue block unless it ended), the continue block (the step, back to the
// header), the merge.
func (c *compiler) forStmt(f *Function, s shaderir.Stmt) error {
	v, ok := c.vars[s.ForVarIndex]
	if !ok {
		return fmt.Errorf("spirv: the for loop's counter %d undeclared", s.ForVarIndex)
	}
	initC, err := c.forConst(s.ForVarType, s.ForInit)
	if err != nil {
		return err
	}
	endC, err := c.forConst(s.ForVarType, s.ForEnd)
	if err != nil {
		return err
	}
	deltaC, err := c.forConst(s.ForVarType, s.ForDelta)
	if err != nil {
		return err
	}
	st, _ := c.typ(s.ForVarType)
	f.Store(v.ptr, initC)
	header, condL, bodyL, contL, mergeL := c.m.ID(), c.m.ID(), c.m.ID(), c.m.ID(), c.m.ID()
	f.Branch(header)
	f.LabelAt(header)
	f.LoopMerge(mergeL, contL)
	f.Branch(condL)
	f.LabelAt(condL)
	cur := value{f.Load(st, v.ptr), s.ForVarType}
	cond, ok := c.comparison(f, s.ForOp, cur, value{endC, s.ForVarType})
	if !ok {
		return fmt.Errorf("spirv: a for loop's operator %d", s.ForOp)
	}
	f.BranchConditional(cond.id, bodyL, mergeL)
	f.LabelAt(bodyL)
	c.loops = append(c.loops, [2]uint32{contL, mergeL})
	ended, err := c.stmts(f, s.Blocks[0])
	c.loops = c.loops[:len(c.loops)-1]
	if err != nil {
		return err
	}
	if !ended {
		f.Branch(contL)
	}
	f.LabelAt(contL)
	next := f.Load(st, v.ptr)
	if s.ForVarType.Main == shaderir.Int {
		next = f.IAdd(st, next, deltaC)
	} else {
		next = f.FAdd(st, next, deltaC)
	}
	f.Store(v.ptr, next)
	f.Branch(header)
	f.LabelAt(mergeL)
	return nil
}

// expr evaluates an expression to a value.
func (c *compiler) expr(f *Function, e shaderir.Expr) (value, error) {
	switch e.Type {
	case shaderir.NumberExpr:
		if e.Const.Kind() == constant.Bool {
			return value{c.m.ConstantBool(constant.BoolVal(e.Const)), shaderir.Type{Main: shaderir.Bool}}, nil
		}
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
	case shaderir.TextureVariable: // the variable itself: __texelAt loads it
		return value{c.texture(e.Index), shaderir.Type{Main: shaderir.Texture}}, nil
	case shaderir.UniformVariable:
		if e.Index >= len(c.p.Uniforms) {
			return value{}, fmt.Errorf("spirv: unknown uniform %d", e.Index)
		}
		t := c.p.Uniforms[e.Index]
		if t.Main == shaderir.Bool {
			i32 := c.m.TypeInt(32, true)
			ptr := f.AccessChain(c.m.TypePointer(StorageClassUniform, i32), c.uniforms, c.m.ConstantInt32(int32(e.Index)))
			b, _ := c.typ(t)
			return value{f.op(opINotEqual, b, f.Load(i32, ptr), c.m.ConstantInt32(0)), t}, nil
		}
		st, err := c.typ(t)
		if err != nil {
			return value{}, err
		}
		ptr := f.AccessChain(c.m.TypePointer(StorageClassUniform, st), c.uniforms, c.m.ConstantInt32(int32(e.Index)))
		return value{f.Load(st, ptr), t}, nil
	case shaderir.Call:
		callee := e.Exprs[0]
		if callee.Type == shaderir.FunctionExpr {
			return c.callUser(f, callee.Index, e.Exprs[1:])
		}
		if callee.Type != shaderir.BuiltinFuncExpr {
			return value{}, fmt.Errorf("spirv: calls of expression type %d not supported yet", callee.Type)
		}
		if callee.BuiltinFunc == shaderir.TexelAt {
			return c.texelAt(f, e.Exprs[1:])
		}
		if v, ok, err := c.mathBuiltin(f, callee.BuiltinFunc, e.Exprs[1:]); ok || err != nil {
			return v, err
		}
		if callee.BuiltinFunc == shaderir.Len && len(e.Exprs) == 2 { // len(array): its length, a constant
			if a := e.Exprs[1]; a.Type == shaderir.LocalVariable {
				if v, ok := c.vars[a.Index]; ok && v.typ.Main == shaderir.Array {
					return value{c.m.ConstantInt32(int32(v.typ.Length)), shaderir.Type{Main: shaderir.Int}}, nil
				}
			}
			return value{}, fmt.Errorf("spirv: len of this expression not supported yet")
		}
		if callee.BuiltinFunc == shaderir.FrontFacing && len(e.Exprs) == 1 {
			// frontfacing(): counter-clockwise in Ebitengine's pixel space
			// is front, as on OpenGL offscreen — the projection and
			// Vulkan's viewport only scale both axes positively, and the
			// pipelines keep VK_FRONT_FACE_COUNTER_CLOCKWISE.
			b := shaderir.Type{Main: shaderir.Bool}
			bt, _ := c.typ(b)
			if c.frontFacing == 0 {
				v, err := c.global(b, StorageClassInput)
				if err != nil {
					return value{}, err
				}
				c.m.Decorate(v, DecorationBuiltIn, BuiltInFrontFacing)
				c.frontFacing = v
				c.texList = append(c.texList, v)
			}
			return value{f.Load(bt, c.frontFacing), b}, nil
		}
		if (callee.BuiltinFunc == shaderir.FloatF || callee.BuiltinFunc == shaderir.IntF) && len(e.Exprs) == 2 {
			return c.convert(f, callee.BuiltinFunc, e.Exprs[1])
		}
		var t shaderir.Type
		switch callee.BuiltinFunc {
		case shaderir.Vec2F:
			t = shaderir.Type{Main: shaderir.Vec2}
		case shaderir.Vec3F:
			t = shaderir.Type{Main: shaderir.Vec3}
		case shaderir.Vec4F:
			t = shaderir.Type{Main: shaderir.Vec4}
		case shaderir.IVec2F:
			t = shaderir.Type{Main: shaderir.IVec2}
		case shaderir.IVec3F:
			t = shaderir.Type{Main: shaderir.IVec3}
		case shaderir.IVec4F:
			t = shaderir.Type{Main: shaderir.IVec4}
		case shaderir.Mat2F, shaderir.Mat3F, shaderir.Mat4F:
			return c.matrixConstructor(f, callee.BuiltinFunc, e.Exprs[1:])
		default:
			return value{}, fmt.Errorf("spirv: built-in %s not supported yet", callee.BuiltinFunc)
		}
		var parts []uint32
		n := 0
		intVec := isIntish(t)
		for _, a := range e.Exprs[1:] {
			var v value
			var err error
			if intVec && a.Type == shaderir.NumberExpr { // a bare number: an int constant here
				var id uint32
				id, err = c.forConst(shaderir.Type{Main: shaderir.Int}, a.Const)
				v = value{id, shaderir.Type{Main: shaderir.Int}}
			} else {
				v, err = c.expr(f, a)
			}
			if err != nil {
				return value{}, err
			}
			// ivecN(floats) and vecN(ints) convert (OpConvertFToS truncates, as Go's int(x)).
			if intVec && isFloatish(v.typ) {
				ct := shaderir.Type{Main: shaderir.Int}
				if k := v.typ.DwordCount(); k > 1 {
					ct = shaderir.Type{Main: shaderir.IVec2 + shaderir.BasicType(k-2)}
				}
				ctt, _ := c.typ(ct)
				v = value{f.op(opConvertFToS, ctt, v.id), ct}
			} else if !intVec && isIntish(v.typ) {
				ct := shaderir.Type{Main: shaderir.Float}
				if k := v.typ.DwordCount(); k > 1 {
					ct = shaderir.Type{Main: shaderir.Vec2 + shaderir.BasicType(k-2)}
				}
				ctt, _ := c.typ(ct)
				v = value{f.ConvertSToF(ctt, v.id), ct}
			}
			parts = append(parts, v.id)
			n += max(v.typ.DwordCount(), 1)
		}
		st, _ := c.typ(t)
		if len(parts) == 1 && n == t.DwordCount() { // vecN(v) of an N-vector (converted above if need be): v itself
			return value{parts[0], t}, nil
		}
		if n == 1 { // vecN(x): every component x
			for len(parts) < t.DwordCount() {
				parts = append(parts, parts[0])
			}
		} else if n != t.DwordCount() {
			return value{}, fmt.Errorf("spirv: %s with %d components", callee.BuiltinFunc, n)
		}
		return value{f.CompositeConstruct(st, parts...), t}, nil // vector constituents may be vectors
	case shaderir.Unary:
		v, err := c.expr(f, e.Exprs[0])
		if err != nil {
			return value{}, err
		}
		t, _ := c.typ(v.typ)
		switch {
		case e.Op == shaderir.NotOp && v.typ.Main == shaderir.Bool:
			return value{f.op(opLogicalNot, t, v.id), v.typ}, nil
		case e.Op == shaderir.Sub && isFloatish(v.typ):
			return value{f.op(opFNegate, t, v.id), v.typ}, nil
		case e.Op == shaderir.Sub && isIntish(v.typ):
			return value{f.op(opSNegate, t, v.id), v.typ}, nil
		case e.Op == shaderir.ComplementOp && isIntish(v.typ): // ^x
			return value{f.op(opNot, t, v.id), v.typ}, nil
		case e.Op == shaderir.Add:
			return v, nil
		}
		return value{}, fmt.Errorf("spirv: unary operator %d on %s not supported yet", e.Op, v.typ.String())
	case shaderir.FieldSelector:
		return c.fieldSelector(f, e)
	case shaderir.Index:
		return c.index(f, e)
	case shaderir.Binary:
		if e.Op == shaderir.AndAnd || e.Op == shaderir.OrOr {
			return c.shortCircuit(f, e)
		}
		l, err := c.expr(f, e.Exprs[0])
		if err != nil {
			return value{}, err
		}
		r, err := c.expr(f, e.Exprs[1])
		if err != nil {
			return value{}, err
		}
		// an untyped number next to an int or int vector is an int (n + 1,
		// v % 3: arithmetic spreads it over the vector)
		intT := shaderir.Type{Main: shaderir.Int}
		if e.Exprs[1].Type == shaderir.NumberExpr && isIntish(l.typ) {
			id, _ := c.forConst(intT, e.Exprs[1].Const)
			r = value{id, intT}
		} else if e.Exprs[0].Type == shaderir.NumberExpr && isIntish(r.typ) {
			id, _ := c.forConst(intT, e.Exprs[0].Const)
			l = value{id, intT}
		}
		if e.Op == shaderir.MatrixMul && l.typ.Main >= shaderir.Mat2 && l.typ.Main <= shaderir.Mat4 && r.typ.Main >= shaderir.Vec2 && r.typ.Main <= shaderir.Vec4 {
			st, _ := c.typ(r.typ)
			return value{f.MatrixTimesVector(st, l.id, r.id), r.typ}, nil
		}
		isMat := func(t shaderir.Type) bool { return t.Main >= shaderir.Mat2 && t.Main <= shaderir.Mat4 }
		isVec := func(t shaderir.Type) bool { return t.Main >= shaderir.Vec2 && t.Main <= shaderir.Vec4 }
		if e.Op == shaderir.MatrixMul && isMat(l.typ) && isMat(r.typ) && l.typ.Main == r.typ.Main { // m * m
			st, _ := c.typ(l.typ)
			return value{f.op(opMatrixTimesMatrix, st, l.id, r.id), l.typ}, nil
		}
		if e.Op == shaderir.MatrixMul && isVec(l.typ) && isMat(r.typ) { // v * m
			st, _ := c.typ(l.typ)
			return value{f.op(opVectorTimesMatrix, st, l.id, r.id), l.typ}, nil
		}
		if e.Op == shaderir.Div && isMat(l.typ) && r.typ.Main == shaderir.Float { // m / s: each column divided (GLSL's component-wise /)
			n := int(l.typ.Main-shaderir.Mat2) + 2
			colT := shaderir.Type{Main: shaderir.Vec2 + (l.typ.Main - shaderir.Mat2)}
			ct, _ := c.typ(colT)
			mt, _ := c.typ(l.typ)
			parts := make([]uint32, n)
			for k := range parts {
				parts[k] = r.id
			}
			spread := f.CompositeConstruct(ct, parts...)
			cols := make([]uint32, n)
			for k := range cols {
				cols[k] = f.FDiv(ct, f.CompositeExtract(ct, l.id, uint32(k)), spread)
			}
			return value{f.CompositeConstruct(mt, cols...), l.typ}, nil
		}
		if (e.Op == shaderir.MatrixMul || e.Op == shaderir.ComponentWiseMul) && (isMat(l.typ) && r.typ.Main == shaderir.Float || l.typ.Main == shaderir.Float && isMat(r.typ)) { // m * s, s * m
			m, s := l, r
			if !isMat(m.typ) {
				m, s = r, l
			}
			st, _ := c.typ(m.typ)
			return value{f.op(opMatrixTimesScalar, st, m.id, s.id), m.typ}, nil
		}

		if v, ok, err := c.arithmetic(f, e.Op, l, r); ok || err != nil {
			return v, err
		}
		if v, ok := c.comparison(f, e.Op, l, r); ok {
			return v, nil
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
	if block == nil {
		return nil, fmt.Errorf("spirv: the program has no fragment function")
	}
	if err := c.userFuncs(block); err != nil {
		return nil, err
	}
	void := c.m.TypeVoid()
	var bodyErr error
	main := c.m.Function(void, c.m.TypeFunction(void), func(f *Function) {
		f.Label()
		for i, t := range block.LocalVars {
			if t.Main == shaderir.None { // a for loop's counter: the for declares it, with its type
				continue
			}
			st, err := c.typ(t)
			if err != nil {
				bodyErr = err
				return
			}
			ptr := f.LocalVariable(c.m.TypePointer(StorageClassFunction, st))
			c.zeroOnEntry(block, ptr, st)
			c.vars[1+nv+i] = variable{ptr: ptr, typ: t, storage: StorageClassFunction}
		}
		if bodyErr = c.declareNested(f, block); bodyErr != nil {
			return
		}
		if bodyErr = c.copyAssignedInputs(f, block); bodyErr != nil {
			return
		}
		bodyErr = c.block(f, block)
	})
	if bodyErr != nil {
		return nil, bodyErr
	}
	c.m.EntryPoint(ExecutionModelFragment, main, "main", append(interfaces, c.texList...)...)
	c.m.ExecutionMode(main, ExecutionModeOriginUpperLeft)
	return c.m.Words(), nil
}

func isFloatish(t shaderir.Type) bool {
	return t.Main == shaderir.Float || (t.Main >= shaderir.Vec2 && t.Main <= shaderir.Vec4)
}

func isIntish(t shaderir.Type) bool {
	return t.Main == shaderir.Int || (t.Main >= shaderir.IVec2 && t.Main <= shaderir.IVec4)
}

// arithmetic is + − * / on floats / float vectors (one type, or a vector with
// a scalar on either side — the scalar spread for + − /, VectorTimesScalar
// for *) and on ints / int vectors of one type (% too: OpSRem, the sign of
// the dividend, as Go's and C's %). ok false: not one of these.
func (c *compiler) arithmetic(f *Function, op shaderir.Op, l, r value) (value, bool, error) {
	if op != shaderir.Add && op != shaderir.Sub && op != shaderir.ComponentWiseMul && op != shaderir.Div &&
		!(isIntish(l.typ) && (op == shaderir.ModOp || op == shaderir.And || op == shaderir.AndNot || op == shaderir.Or || op == shaderir.Xor || op == shaderir.LeftShift || op == shaderir.RightShift)) {
		return value{}, false, nil
	}
	switch {
	case isFloatish(l.typ) && isFloatish(r.typ):
		if l.typ.Main != r.typ.Main { // a vector and a scalar
			vec, sc, scalarLeft := l, r, false
			if l.typ.Main == shaderir.Float {
				vec, sc, scalarLeft = r, l, true
			}
			if sc.typ.Main != shaderir.Float {
				return value{}, true, fmt.Errorf("spirv: %s with %s", l.typ.String(), r.typ.String())
			}
			vt, _ := c.typ(vec.typ)
			if op == shaderir.ComponentWiseMul {
				return value{f.VectorTimesScalar(vt, vec.id, sc.id), vec.typ}, true, nil
			}
			parts := make([]uint32, vec.typ.DwordCount())
			for i := range parts {
				parts[i] = sc.id
			}
			spread := value{f.CompositeConstruct(vt, parts...), vec.typ}
			if scalarLeft {
				l = spread
			} else {
				r = spread
			}
		}
		t, _ := c.typ(l.typ)
		switch op {
		case shaderir.Add:
			return value{f.FAdd(t, l.id, r.id), l.typ}, true, nil
		case shaderir.Sub:
			return value{f.FSub(t, l.id, r.id), l.typ}, true, nil
		case shaderir.ComponentWiseMul:
			return value{f.FMul(t, l.id, r.id), l.typ}, true, nil
		default:
			return value{f.FDiv(t, l.id, r.id), l.typ}, true, nil
		}
	case isIntish(l.typ) && isIntish(r.typ) && (l.typ.Main == r.typ.Main || l.typ.Main == shaderir.Int || r.typ.Main == shaderir.Int):
		if l.typ.Main != r.typ.Main { // an int with an int vector: spread it
			vec, sc := l, r
			if vec.typ.Main == shaderir.Int {
				vec, sc = r, l
			}
			vt, _ := c.typ(vec.typ)
			parts := make([]uint32, vec.typ.DwordCount())
			for i := range parts {
				parts[i] = sc.id
			}
			spread := value{f.CompositeConstruct(vt, parts...), vec.typ}
			if l.typ.Main == shaderir.Int {
				l = spread
			} else {
				r = spread
			}
		}
		t, _ := c.typ(l.typ)
		switch op {
		case shaderir.And:
			return value{f.BitwiseAnd(t, l.id, r.id), l.typ}, true, nil
		case shaderir.AndNot: // &^: l & ^r
			return value{f.BitwiseAnd(t, l.id, f.op(opNot, t, r.id)), l.typ}, true, nil
		case shaderir.Or:
			return value{f.op(opBitwiseOr, t, l.id, r.id), l.typ}, true, nil
		case shaderir.Xor:
			return value{f.op(opBitwiseXor, t, l.id, r.id), l.typ}, true, nil
		case shaderir.LeftShift:
			return value{f.ShiftLeftLogical(t, l.id, r.id), l.typ}, true, nil
		case shaderir.RightShift:
			return value{f.op(opShiftRightArithmetic, t, l.id, r.id), l.typ}, true, nil
		case shaderir.Add:
			return value{f.IAdd(t, l.id, r.id), l.typ}, true, nil
		case shaderir.Sub:
			return value{f.ISub(t, l.id, r.id), l.typ}, true, nil
		case shaderir.ComponentWiseMul:
			return value{f.IMul(t, l.id, r.id), l.typ}, true, nil
		case shaderir.ModOp:
			return value{f.op(opSRem, t, l.id, r.id), l.typ}, true, nil
		default:
			return value{f.SDiv(t, l.id, r.id), l.typ}, true, nil
		}
	}
	return value{}, false, nil
}

// userFuncs compiles the user functions the block reaches, callees first —
// each before any code that calls it, as a module's functions can't
// interleave.
func (c *compiler) userFuncs(root *shaderir.Block) error {
	var visit func(b *shaderir.Block) error
	var visitExpr func(e shaderir.Expr) error
	visiting := map[int]bool{}
	visitExpr = func(e shaderir.Expr) error {
		if e.Type == shaderir.Call && len(e.Exprs) > 0 && e.Exprs[0].Type == shaderir.FunctionExpr {
			idx := e.Exprs[0].Index
			if _, done := c.funcs[idx]; !done {
				if visiting[idx] {
					return fmt.Errorf("spirv: recursion through function %d", idx)
				}
				visiting[idx] = true
				fn := c.funcByIndex(idx)
				if fn == nil {
					return fmt.Errorf("spirv: unknown function %d", idx)
				}
				if err := visit(fn.Block); err != nil {
					return err
				}
				if err := c.userFunc(fn); err != nil {
					return err
				}
			}
		}
		for _, s := range e.Exprs {
			if err := visitExpr(s); err != nil {
				return err
			}
		}
		return nil
	}
	visit = func(b *shaderir.Block) error {
		if b == nil {
			return nil
		}
		for _, s := range b.Stmts {
			for _, e := range s.Exprs {
				if err := visitExpr(e); err != nil {
					return err
				}
			}
			for _, bb := range s.Blocks {
				if err := visit(bb); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return visit(root)
}

func (c *compiler) funcByIndex(idx int) *shaderir.Func {
	for i := range c.p.Funcs {
		if c.p.Funcs[i].Index == idx {
			return &c.p.Funcs[i]
		}
	}
	return nil
}

// userFunc compiles one user function: its in-parameters by value (each
// copied into a Function variable, so the body may assign it), its locals,
// its body. PARTIAL: out-parameters.
func (c *compiler) userFunc(fn *shaderir.Func) error {
	ret := c.m.TypeVoid()
	if fn.Return.Main != shaderir.None {
		var err error
		if ret, err = c.typ(fn.Return); err != nil {
			return err
		}
	}
	params := make([]uint32, len(fn.InParams), len(fn.InParams)+len(fn.OutParams))
	for i, t := range fn.InParams {
		var err error
		if params[i], err = c.typ(t); err != nil {
			return err
		}
	}
	outTypes := make([]uint32, len(fn.OutParams)) // out-parameters: pointers to the caller's Function variables
	for j, t := range fn.OutParams {
		st, err := c.typ(t)
		if err != nil {
			return err
		}
		outTypes[j] = st
		params = append(params, c.m.TypePointer(StorageClassFunction, st))
	}
	saved, savedResult, savedIn, savedRet := c.vars, c.result, c.inFunc, c.retType
	c.vars, c.result, c.inFunc, c.retType = map[int]variable{}, 0, true, fn.Return
	defer func() { c.vars, c.result, c.inFunc, c.retType = saved, savedResult, savedIn, savedRet }()
	var bodyErr error
	id := c.m.Function(ret, c.m.TypeFunction(ret, params...), func(f *Function) {
		ids := make([]uint32, len(params))
		for i, t := range params {
			ids[i] = f.Parameter(t)
		}
		f.Label()
		for i, t := range fn.InParams {
			ptr := f.LocalVariable(c.m.TypePointer(StorageClassFunction, params[i]))
			c.vars[i] = variable{ptr: ptr, typ: t, storage: StorageClassFunction}
		}
		for j, t := range fn.OutParams { // written straight through the pointer
			c.vars[len(fn.InParams)+j] = variable{ptr: ids[len(fn.InParams)+j], typ: t, storage: StorageClassFunction}
		}
		_ = outTypes
		for i, t := range fn.Block.LocalVars {
			if t.Main == shaderir.None { // a for loop's counter: the for declares it, with its type
				continue
			}
			st, err := c.typ(t)
			if err != nil {
				bodyErr = err
				return
			}
			ptr := f.LocalVariable(c.m.TypePointer(StorageClassFunction, st))
			c.zeroOnEntry(fn.Block, ptr, st)
			c.vars[fn.Block.LocalVarIndexOffset+i] = variable{ptr: ptr, typ: t, storage: StorageClassFunction}
		}
		if bodyErr = c.declareNested(f, fn.Block); bodyErr != nil {
			return
		}
		for i := range fn.InParams {
			f.Store(c.vars[i].ptr, ids[i])
		}
		bodyErr = c.block(f, fn.Block)
	})
	if bodyErr != nil {
		return bodyErr
	}
	c.funcs[fn.Index] = id
	return nil
}

// callUser is a call of a compiled user function.
func (c *compiler) callUser(f *Function, idx int, args []shaderir.Expr) (value, error) {
	id, ok := c.funcs[idx]
	fn := c.funcByIndex(idx)
	if !ok || fn == nil {
		return value{}, fmt.Errorf("spirv: function %d not compiled", idx)
	}
	var ids []uint32
	for i, a := range args {
		if i >= len(fn.InParams) { // an out-argument: the caller's variable itself
			if a.Type != shaderir.LocalVariable {
				return value{}, fmt.Errorf("spirv: an out-argument of expression type %d", a.Type)
			}
			v, ok := c.vars[a.Index]
			if !ok || v.storage != StorageClassFunction {
				return value{}, fmt.Errorf("spirv: an out-argument into local %d (not a Function variable) not supported yet", a.Index)
			}
			ids = append(ids, v.ptr)
			continue
		}
		if a.Type == shaderir.NumberExpr && i < len(fn.InParams) && (fn.InParams[i].Main == shaderir.Int || fn.InParams[i].Main == shaderir.Float) {
			id, err := c.forConst(fn.InParams[i], a.Const) // an untyped number takes the parameter's type
			if err != nil {
				return value{}, err
			}
			ids = append(ids, id)
			continue
		}
		v, err := c.expr(f, a)
		if err != nil {
			return value{}, err
		}
		ids = append(ids, v.id)
	}
	ret := c.m.TypeVoid()
	if fn.Return.Main != shaderir.None {
		ret, _ = c.typ(fn.Return)
	}
	return value{f.Call(ret, id, ids...), fn.Return}, nil
}

// texture is texture i's combined image sampler variable (UniformConstant;
// set 0, binding 1 + i — the uniform block is binding 0), made on first use.
func (c *compiler) texture(i int) uint32 {
	if v, ok := c.textures[i]; ok {
		return v
	}
	simg := c.m.TypeSampledImage(c.m.TypeImage2D())
	v := c.m.Variable(c.m.TypePointer(StorageClassUniformConstant, simg), StorageClassUniformConstant)
	c.m.Decorate(v, DecorationDescriptorSet, 0)
	c.m.Decorate(v, DecorationBinding, uint32(1+i))
	c.textures[i] = v
	c.texList = append(c.texList, v)
	return v
}

// texelAt is __texelAt(texture, position): the texel at floor(position) in
// pixels — sampled through Ebitengine's texel sampler (unnormalized,
// nearest, transparent outside; vk.CreateTexelSampler) at LOD 0, the
// position floored first as the GLSL and Metal backends do (a negative
// position is then outside, not rounded in).
func (c *compiler) texelAt(f *Function, args []shaderir.Expr) (value, error) {
	if len(args) != 2 || args[0].Type != shaderir.TextureVariable {
		return value{}, fmt.Errorf("spirv: __texelAt takes a texture variable and a position")
	}
	pos, err := c.expr(f, args[1])
	if err != nil {
		return value{}, err
	}
	if pos.typ.Main != shaderir.Vec2 {
		return value{}, fmt.Errorf("spirv: __texelAt at a %s", pos.typ.String())
	}
	vec2, _ := c.typ(pos.typ)
	vec4 := shaderir.Type{Main: shaderir.Vec4}
	vt, _ := c.typ(vec4)
	simg := c.m.TypeSampledImage(c.m.TypeImage2D())
	img := f.Load(simg, c.texture(args[0].Index))
	floored := f.GLSL(vec2, GLSLFloor, pos.id)
	return value{f.ImageSampleExplicitLod(vt, img, floored, c.m.ConstantFloat32(0)), vec4}, nil
}

// glslFloat maps the IR's float built-ins onto GLSL.std.450 (each taking and
// giving floats or float vectors of the first argument's type; Length and
// Distance give a float).
var glslFloat = map[shaderir.BuiltinFunc]uint32{
	shaderir.Radians: 11, shaderir.Degrees: 12, shaderir.Sin: 13, shaderir.Cos: 14, shaderir.Tan: 15,
	shaderir.Asin: 16, shaderir.Acos: 17, shaderir.Atan: 18, shaderir.Atan2: 25, shaderir.Pow: 26,
	shaderir.Exp: 27, shaderir.Log: 28, shaderir.Exp2: 29, shaderir.Log2: 30, shaderir.Sqrt: 31,
	shaderir.Inversesqrt: 32, shaderir.Abs: 4, shaderir.Sign: 6, shaderir.Floor: 8, shaderir.Ceil: 9,
	shaderir.Fract: 10, shaderir.Min: 37, shaderir.Max: 40, shaderir.Clamp: 43, shaderir.Mix: 46,
	shaderir.Step: 48, shaderir.Smoothstep: 49, shaderir.Length: 66, shaderir.Distance: 67,
	shaderir.Cross: 68, shaderir.Normalize: 69, shaderir.Reflect: 71, shaderir.Refract: 72,
}

// mathBuiltin is a float built-in: GLSL.std.450 (scalars spread to the
// vector's type where GLSL wants one type: min, max, clamp, mix, step,
// smoothstep), mod as OpFMod (GLSL's x − y·floor(x / y): the sign of y), dot
// as OpDot. ok false: not a float built-in. PARTIAL: their integer forms.
func (c *compiler) mathBuiltin(f *Function, fn shaderir.BuiltinFunc, args []shaderir.Expr) (value, bool, error) {
	ext, isExt := glslFloat[fn]
	derivative := map[shaderir.BuiltinFunc]uint16{shaderir.Dfdx: opDPdx, shaderir.Dfdy: opDPdy, shaderir.Fwidth: opFwidth}
	if op, ok := derivative[fn]; ok { // fragment only: OpDPdx / OpDPdy / OpFwidth on the value's type
		if len(args) != 1 {
			return value{}, true, fmt.Errorf("spirv: %s takes one argument", fn)
		}
		v, err := c.expr(f, args[0])
		if err != nil {
			return value{}, true, err
		}
		vt, _ := c.typ(v.typ)
		return value{f.op(op, vt, v.id), v.typ}, true, nil
	}
	if !isExt && fn != shaderir.Mod && fn != shaderir.Dot {
		return value{}, false, nil
	}
	vals := make([]value, len(args))
	for i, a := range args {
		v, err := c.expr(f, a)
		if err != nil {
			return value{}, true, err
		}
		vals[i] = v
	}
	if len(vals) > 0 && isIntish(vals[0].typ) { // the integer forms: SAbs, SSign, SMin, SMax, SClamp
		intExt := map[shaderir.BuiltinFunc]uint32{shaderir.Abs: 5, shaderir.Sign: 7, shaderir.Min: 39, shaderir.Max: 42, shaderir.Clamp: 45}
		e, ok := intExt[fn]
		if !ok {
			return value{}, true, fmt.Errorf("spirv: %s on %s not supported yet", fn, vals[0].typ.String())
		}
		t := vals[0].typ
		tt, _ := c.typ(t)
		ids := make([]uint32, len(vals))
		for i, v := range vals {
			ids[i] = v.id
			if args[i].Type == shaderir.NumberExpr { // a bare number among ints: an int
				ids[i], _ = c.forConst(shaderir.Type{Main: shaderir.Int}, args[i].Const)
			}
		}
		return value{glslFold(f, fn, tt, e, ids), t}, true, nil
	}
	for _, v := range vals {
		if !isFloatish(v.typ) {
			return value{}, true, fmt.Errorf("spirv: %s on %s not supported yet", fn, v.typ.String())
		}
	}
	if len(vals) == 0 {
		return value{}, true, fmt.Errorf("spirv: %s without arguments", fn)
	}
	// the widest argument's type: the others (scalars) spread to it
	t := vals[0].typ
	for _, v := range vals[1:] {
		if v.typ.Main != shaderir.Float {
			t = v.typ
		}
	}
	tt, _ := c.typ(t)
	ids := make([]uint32, len(vals))
	for i, v := range vals {
		ids[i] = v.id
		if v.typ.Main == shaderir.Float && t.Main != shaderir.Float && fn != shaderir.Refract {
			parts := make([]uint32, t.DwordCount())
			for j := range parts {
				parts[j] = v.id
			}
			ids[i] = f.CompositeConstruct(tt, parts...)
		}
	}
	switch fn {
	case shaderir.Mod:
		return value{f.op(opFMod, tt, ids[0], ids[1]), t}, true, nil
	case shaderir.Dot, shaderir.Length, shaderir.Distance:
		ft := c.m.TypeFloat(32)
		float := shaderir.Type{Main: shaderir.Float}
		if fn == shaderir.Dot {
			return value{f.op(opDot, ft, ids[0], ids[1]), float}, true, nil
		}
		return value{f.GLSL(ft, ext, ids...), float}, true, nil
	}
	return value{glslFold(f, fn, tt, ext, ids), t}, true, nil
}

// glslFold is the GLSL.std.450 instruction e on the arguments — min and
// max, variadic in Kage but two-operand there, folded left: min(a, b, c) is
// min(min(a, b), c).
func glslFold(f *Function, fn shaderir.BuiltinFunc, typ, e uint32, ids []uint32) uint32 {
	if (fn != shaderir.Min && fn != shaderir.Max) || len(ids) <= 2 {
		return f.GLSL(typ, e, ids...)
	}
	r := f.GLSL(typ, e, ids[0], ids[1])
	for _, id := range ids[2:] {
		r = f.GLSL(typ, e, r, id)
	}
	return r
}

// fieldSelector is v.xyzw: one component (CompositeExtract) or several
// (VectorShuffle) of a vector.
func (c *compiler) fieldSelector(f *Function, e shaderir.Expr) (value, error) {
	v, err := c.expr(f, e.Exprs[0])
	if err != nil {
		return value{}, err
	}
	sw := e.Exprs[1].Swizzling
	idx := make([]uint32, len(sw))
	for i, ch := range sw {
		switch ch {
		case 'x':
			idx[i] = 0
		case 'y':
			idx[i] = 1
		case 'z':
			idx[i] = 2
		case 'w':
			idx[i] = 3
		default:
			return value{}, fmt.Errorf("spirv: swizzle %q", sw)
		}
	}
	isInt := isIntish(v.typ)
	scalar := shaderir.Type{Main: shaderir.Float}
	if isInt {
		scalar = shaderir.Type{Main: shaderir.Int}
	}
	if len(idx) == 1 {
		st, _ := c.typ(scalar)
		return value{f.CompositeExtract(st, v.id, idx[0]), scalar}, nil
	}
	rt := shaderir.Type{Main: shaderir.Vec2 + shaderir.BasicType(len(idx)-2)}
	if isInt {
		rt = shaderir.Type{Main: shaderir.IVec2 + shaderir.BasicType(len(idx)-2)}
	}
	st, _ := c.typ(rt)
	return value{f.VectorShuffle(st, v.id, v.id, idx...), rt}, nil
}

// index is a[i] for a uniform array (an access chain into the block with the
// index — constant or not) or a vector (a constant index: CompositeExtract).
// PARTIAL: local arrays, matrices.
func (c *compiler) index(f *Function, e shaderir.Expr) (value, error) {
	base, ix := e.Exprs[0], e.Exprs[1]
	if base.Type == shaderir.UniformVariable && base.Index < len(c.p.Uniforms) && c.p.Uniforms[base.Index].Main == shaderir.Array {
		et := c.p.Uniforms[base.Index].Sub[0]
		st, err := c.typ(et)
		if err != nil {
			return value{}, err
		}
		i, err := c.expr(f, ix)
		if err != nil {
			return value{}, err
		}
		if ix.Type == shaderir.NumberExpr { // an int index, not the float a bare number becomes
			n, _ := constant.Int64Val(constant.ToInt(ix.Const))
			i = value{c.m.ConstantInt32(int32(n)), shaderir.Type{Main: shaderir.Int}}
		}
		if et.Main == shaderir.Bool { // the block holds bools as ints (blockType): read as != 0
			i32 := c.m.TypeInt(32, true)
			ptr := f.AccessChain(c.m.TypePointer(StorageClassUniform, i32), c.uniforms, c.m.ConstantInt32(int32(base.Index)), i.id)
			return value{f.op(opINotEqual, st, f.Load(i32, ptr), c.m.ConstantInt32(0)), et}, nil
		}
		ptr := f.AccessChain(c.m.TypePointer(StorageClassUniform, st), c.uniforms, c.m.ConstantInt32(int32(base.Index)), i.id)
		return value{f.Load(st, ptr), et}, nil
	}
	if base.Type == shaderir.LocalVariable {
		if v, ok := c.vars[base.Index]; ok && v.typ.Main == shaderir.Array {
			ptr, et, err := c.elementPtr(f, v, ix)
			if err != nil {
				return value{}, err
			}
			st, _ := c.typ(et)
			return value{f.Load(st, ptr), et}, nil
		}
	}
	v, err := c.expr(f, base)
	if err != nil {
		return value{}, err
	}
	if ix.Type == shaderir.NumberExpr && (isFloatish(v.typ) || isIntish(v.typ)) && v.typ.DwordCount() > 1 {
		n, _ := constant.Int64Val(constant.ToInt(ix.Const))
		et := shaderir.Type{Main: shaderir.Float}
		if isIntish(v.typ) {
			et = shaderir.Type{Main: shaderir.Int}
		}
		st, _ := c.typ(et)
		return value{f.CompositeExtract(st, v.id, uint32(n)), et}, nil
	}
	if v.typ.Main >= shaderir.Mat2 && v.typ.Main <= shaderir.Mat4 { // m[i]: a column
		et := shaderir.Type{Main: shaderir.Vec2 + (v.typ.Main - shaderir.Mat2)}
		st, _ := c.typ(et)
		if ix.Type == shaderir.NumberExpr {
			n, _ := constant.Int64Val(constant.ToInt(ix.Const))
			return value{f.CompositeExtract(st, v.id, uint32(n)), et}, nil
		}
		i, err := c.expr(f, ix) // a dynamic index: the column chosen by selects (no variable — they must open the function)
		if err != nil {
			return value{}, err
		}
		bt, _ := c.typ(shaderir.Type{Main: shaderir.Bool})
		n := int(v.typ.Main-shaderir.Mat2) + 2
		col := f.CompositeExtract(st, v.id, 0)
		for k := 1; k < n; k++ {
			is := f.op(opIEqual, bt, i.id, c.m.ConstantInt32(int32(k)))
			col = f.op(opSelect, st, is, f.CompositeExtract(st, v.id, uint32(k)), col)
		}
		return value{col, et}, nil
	}
	if (isFloatish(v.typ) || isIntish(v.typ)) && v.typ.DwordCount() > 1 { // v[i], a dynamic index
		i, err := c.expr(f, ix)
		if err != nil {
			return value{}, err
		}
		et := shaderir.Type{Main: shaderir.Float}
		if isIntish(v.typ) {
			et = shaderir.Type{Main: shaderir.Int}
		}
		st, _ := c.typ(et)
		return value{f.op(opVectorExtractDynamic, st, v.id, i.id), et}, nil
	}
	return value{}, fmt.Errorf("spirv: indexing %s not supported yet", v.typ.String())
}

// comparison is < <= > >= == != on two scalars of one type (floats ordered,
// != unordered — as glslang writes GLSL's; ints signed), giving a bool.
// Vectors: == and != (VectorEqualOp / VectorNotEqualOp), all or any.
func (c *compiler) comparison(f *Function, op shaderir.Op, l, r value) (value, bool) {
	if (op == shaderir.VectorEqualOp || op == shaderir.VectorNotEqualOp) && l.typ.Main == r.typ.Main && l.typ.DwordCount() > 1 && (isFloatish(l.typ) || isIntish(l.typ)) {
		// v == w: every component equal (OpAll); v != w: any differs (OpAny).
		n := uint32(l.typ.DwordCount())
		bt, _ := c.typ(shaderir.Type{Main: shaderir.Bool})
		bv := c.m.TypeVector(bt, n)
		cmp, reduce := uint16(opFOrdEqual), uint16(155)
		if op == shaderir.VectorNotEqualOp {
			cmp, reduce = opFUnordNotEqual, 154
		}
		if isIntish(l.typ) {
			cmp = opIEqual
			if op == shaderir.VectorNotEqualOp {
				cmp = opINotEqual
			}
		}
		return value{f.op(reduce, bt, f.op(cmp, bv, l.id, r.id)), shaderir.Type{Main: shaderir.Bool}}, true
	}
	if l.typ.Main != r.typ.Main || (l.typ.Main != shaderir.Float && l.typ.Main != shaderir.Int && l.typ.Main != shaderir.Bool) {
		return value{}, false
	}
	float := map[shaderir.Op]uint16{shaderir.LessThanOp: opFOrdLessThan, shaderir.LessThanEqualOp: opFOrdLessThanEqual,
		shaderir.GreaterThanOp: opFOrdGreaterThan, shaderir.GreaterThanEqualOp: opFOrdGreaterThanEqual,
		shaderir.EqualOp: opFOrdEqual, shaderir.NotEqualOp: opFUnordNotEqual}
	integer := map[shaderir.Op]uint16{shaderir.LessThanOp: opSLessThan, shaderir.LessThanEqualOp: opSLessThanEqual,
		shaderir.GreaterThanOp: opSGreaterThan, shaderir.GreaterThanEqualOp: opSGreaterThanEqual,
		shaderir.EqualOp: opIEqual, shaderir.NotEqualOp: opINotEqual}
	table := float
	if l.typ.Main == shaderir.Int {
		table = integer
	}
	if l.typ.Main == shaderir.Bool {
		table = map[shaderir.Op]uint16{shaderir.EqualOp: 164, shaderir.NotEqualOp: 165} // OpLogicalEqual / NotEqual
	}
	o, ok := table[op]
	if !ok {
		return value{}, false
	}
	b := shaderir.Type{Main: shaderir.Bool}
	bt, _ := c.typ(b)
	return value{f.op(o, bt, l.id, r.id), b}, true
}

// shortCircuit is && and ||: the right side only when it matters — its own
// block in a structured selection, the result an OpPhi of the left side's
// value (skipped) or the right's.
func (c *compiler) shortCircuit(f *Function, e shaderir.Expr) (value, error) {
	l, err := c.expr(f, e.Exprs[0])
	if err != nil {
		return value{}, err
	}
	if l.typ.Main != shaderir.Bool {
		return value{}, fmt.Errorf("spirv: && / || on %s", l.typ.String())
	}
	from := f.Current()
	rightL, mergeL := c.m.ID(), c.m.ID()
	f.SelectionMerge(mergeL)
	if e.Op == shaderir.AndAnd {
		f.BranchConditional(l.id, rightL, mergeL)
	} else {
		f.BranchConditional(l.id, mergeL, rightL)
	}
	f.LabelAt(rightL)
	r, err := c.expr(f, e.Exprs[1])
	if err != nil {
		return value{}, err
	}
	rightEnd := f.Current() // the right side may have opened blocks of its own
	f.Branch(mergeL)
	f.LabelAt(mergeL)
	bt, _ := c.typ(l.typ)
	return value{f.Phi(bt, l.id, from, r.id, rightEnd), l.typ}, nil
}

// convert is float(x) / int(x) of a scalar: OpConvertSToF / OpConvertFToS
// (truncating, as Go and GLSL do), or the value itself when it already has
// the type; a bare number becomes a constant of the type.
func (c *compiler) convert(f *Function, fn shaderir.BuiltinFunc, arg shaderir.Expr) (value, error) {
	to := shaderir.Type{Main: shaderir.Float}
	if fn == shaderir.IntF {
		to = shaderir.Type{Main: shaderir.Int}
	}
	if arg.Type == shaderir.NumberExpr {
		id, err := c.forConst(to, arg.Const)
		return value{id, to}, err
	}
	v, err := c.expr(f, arg)
	if err != nil {
		return value{}, err
	}
	if v.typ.Main == to.Main {
		return v, nil
	}
	tt, _ := c.typ(to)
	switch {
	case to.Main == shaderir.Float && v.typ.Main == shaderir.Int:
		return value{f.ConvertSToF(tt, v.id), to}, nil
	case to.Main == shaderir.Int && v.typ.Main == shaderir.Float:
		return value{f.op(opConvertFToS, tt, v.id), to}, nil
	}
	return value{}, fmt.Errorf("spirv: %s(%s) not supported yet", fn, v.typ.String())
}

// CompileLibrary compiles every function of a program — reachable or not —
// into one module with an empty fragment entry point: for checking the
// backend's coverage of the language (and validating it) on programs that are
// only functions, like the shader package's test programs.
func CompileLibrary(p *shaderir.Program) ([]uint32, error) {
	c := newCompiler(p)
	if err := c.uniformBlock(); err != nil {
		return nil, err
	}
	for i := range p.Funcs {
		fn := &p.Funcs[i]
		if _, done := c.funcs[fn.Index]; done {
			continue
		}
		if err := c.userFuncs(fn.Block); err != nil { // its callees first
			return nil, err
		}
		if _, done := c.funcs[fn.Index]; done {
			continue
		}
		if err := c.userFunc(fn); err != nil {
			return nil, fmt.Errorf("function %d: %w", fn.Index, err)
		}
	}
	void := c.m.TypeVoid()
	main := c.m.Function(void, c.m.TypeFunction(void), func(f *Function) {
		f.Label()
		f.Return()
	})
	ifaces := c.texList
	if c.uniforms != 0 {
		ifaces = append(ifaces, c.uniforms)
	}
	c.m.EntryPoint(ExecutionModelFragment, main, "main", ifaces...)
	c.m.ExecutionMode(main, ExecutionModeOriginUpperLeft)
	return c.m.Words(), nil
}

// swizzleIndices are a swizzle's component numbers (xyzw).
func swizzleIndices(sw string) ([]uint32, error) {
	idx := make([]uint32, len(sw))
	for i, ch := range sw {
		switch ch {
		case 'x':
			idx[i] = 0
		case 'y':
			idx[i] = 1
		case 'z':
			idx[i] = 2
		case 'w':
			idx[i] = 3
		default:
			return nil, fmt.Errorf("spirv: swizzle %q", sw)
		}
	}
	return idx, nil
}

// assignField is v.x = e (one component: stored through an access chain) or
// v.xy = e (several: the vector loaded, the new components shuffled in,
// stored back) for a local vector variable.
func (c *compiler) assignField(f *Function, lhs, rhsExpr shaderir.Expr) error {
	base := lhs.Exprs[0]
	if base.Type != shaderir.LocalVariable {
		return fmt.Errorf("spirv: assignment to a field of expression type %d not supported yet", base.Type)
	}
	v, ok := c.vars[base.Index]
	if !ok || v.input {
		return fmt.Errorf("spirv: assignment to a field of local %d (an attribute or unknown) not supported yet", base.Index)
	}
	idx, err := swizzleIndices(lhs.Exprs[1].Swizzling)
	if err != nil {
		return err
	}
	scalar := shaderir.Type{Main: shaderir.Float}
	if isIntish(v.typ) {
		scalar = shaderir.Type{Main: shaderir.Int}
	}
	var rhs value
	if rhsExpr.Type == shaderir.NumberExpr && len(idx) == 1 {
		id, err := c.forConst(scalar, rhsExpr.Const)
		if err != nil {
			return err
		}
		rhs = value{id, scalar}
	} else if rhs, err = c.expr(f, rhsExpr); err != nil {
		return err
	}
	if len(idx) == 1 {
		st, _ := c.typ(scalar)
		ptr := f.AccessChain(c.m.TypePointer(v.storage, st), v.ptr, c.m.ConstantInt32(int32(idx[0])))
		f.Store(ptr, rhs.id)
		return nil
	}
	vt, err := c.typ(v.typ)
	if err != nil {
		return err
	}
	n := uint32(v.typ.DwordCount())
	comps := make([]uint32, n)
	for i := range comps {
		comps[i] = uint32(i) // the old component …
	}
	for k, ci := range idx {
		comps[ci] = n + uint32(k) // … or the new one from the right side
	}
	old := f.Load(vt, v.ptr)
	f.Store(v.ptr, f.VectorShuffle(vt, old, rhs.id, comps...))
	return nil
}

// matrixConstructor is mat2 / mat3 / mat4 of a scalar (that diagonal, zeros
// elsewhere), of its column vectors, or of all its components in column
// order (a mix of scalars and vectors filling the columns).
func (c *compiler) matrixConstructor(f *Function, fn shaderir.BuiltinFunc, args []shaderir.Expr) (value, error) {
	n := map[shaderir.BuiltinFunc]int{shaderir.Mat2F: 2, shaderir.Mat3F: 3, shaderir.Mat4F: 4}[fn]
	mt := shaderir.Type{Main: shaderir.Mat2 + shaderir.BasicType(n-2)}
	colT := shaderir.Type{Main: shaderir.Vec2 + shaderir.BasicType(n-2)}
	mtt, _ := c.typ(mt)
	ct, _ := c.typ(colT)
	f32 := c.m.TypeFloat(32)
	var vals []value
	for _, a := range args {
		if a.Type == shaderir.NumberExpr {
			id, err := c.forConst(shaderir.Type{Main: shaderir.Float}, a.Const)
			if err != nil {
				return value{}, err
			}
			vals = append(vals, value{id, shaderir.Type{Main: shaderir.Float}})
			continue
		}
		v, err := c.expr(f, a)
		if err != nil {
			return value{}, err
		}
		vals = append(vals, v)
	}
	if len(vals) == 1 && vals[0].typ.Main == shaderir.Float { // the diagonal
		zero := c.m.ConstantFloat32(0)
		cols := make([]uint32, n)
		for i := range cols {
			parts := make([]uint32, n)
			for j := range parts {
				parts[j] = zero
				if i == j {
					parts[j] = vals[0].id
				}
			}
			cols[i] = f.CompositeConstruct(ct, parts...)
		}
		return value{f.CompositeConstruct(mtt, cols...), mt}, nil
	}
	if len(vals) == 1 && vals[0].typ.Main == mt.Main { // a copy
		return vals[0], nil
	}
	// the components in column order, vectors spread into their scalars
	var flat []uint32
	for _, v := range vals {
		switch {
		case v.typ.Main == shaderir.Float:
			flat = append(flat, v.id)
		case isFloatish(v.typ):
			for i := 0; i < v.typ.DwordCount(); i++ {
				flat = append(flat, f.CompositeExtract(f32, v.id, uint32(i)))
			}
		default:
			return value{}, fmt.Errorf("spirv: %s of %s not supported yet", fn, v.typ.String())
		}
	}
	if len(flat) != n*n {
		return value{}, fmt.Errorf("spirv: %s with %d components", fn, len(flat))
	}
	cols := make([]uint32, n)
	for i := range cols {
		cols[i] = f.CompositeConstruct(ct, flat[i*n:i*n+n]...)
	}
	return value{f.CompositeConstruct(mtt, cols...), mt}, nil
}

// elementPtr is a pointer to element ix of a local array variable (the
// index an int: a bare number becomes an int constant).
func (c *compiler) elementPtr(f *Function, v variable, ix shaderir.Expr) (uint32, shaderir.Type, error) {
	et := v.typ.Sub[0]
	st, err := c.typ(et)
	if err != nil {
		return 0, et, err
	}
	var idx uint32
	if ix.Type == shaderir.NumberExpr {
		n, _ := constant.Int64Val(constant.ToInt(ix.Const))
		idx = c.m.ConstantInt32(int32(n))
	} else {
		i, err := c.expr(f, ix)
		if err != nil {
			return 0, et, err
		}
		if i.typ.Main != shaderir.Int {
			return 0, et, fmt.Errorf("spirv: an array index of type %s", i.typ.String())
		}
		idx = i.id
	}
	return f.AccessChain(c.m.TypePointer(v.storage, st), v.ptr, idx), et, nil
}

// assignIndex is a[i] = e for a local array.
func (c *compiler) assignIndex(f *Function, lhs, rhsExpr shaderir.Expr) error {
	base := lhs.Exprs[0]
	if base.Type != shaderir.LocalVariable {
		return fmt.Errorf("spirv: assignment to an index of expression type %d not supported yet", base.Type)
	}
	v, ok := c.vars[base.Index]
	if !ok || v.input {
		return fmt.Errorf("spirv: assignment to an index of local %d (unknown or an input) not supported yet", base.Index)
	}
	var ptr uint32
	var et shaderir.Type
	var err error
	switch {
	case v.typ.Main == shaderir.Array:
		if ptr, et, err = c.elementPtr(f, v, lhs.Exprs[1]); err != nil {
			return err
		}
	case (isFloatish(v.typ) || isIntish(v.typ)) && v.typ.DwordCount() > 1: // v[i] of a vector: that component
		et = shaderir.Type{Main: shaderir.Float}
		if isIntish(v.typ) {
			et = shaderir.Type{Main: shaderir.Int}
		}
		st, _ := c.typ(et)
		var idx uint32
		if ix := lhs.Exprs[1]; ix.Type == shaderir.NumberExpr {
			n, _ := constant.Int64Val(constant.ToInt(ix.Const))
			idx = c.m.ConstantInt32(int32(n))
		} else {
			i, err := c.expr(f, ix)
			if err != nil {
				return err
			}
			idx = i.id
		}
		ptr = f.AccessChain(c.m.TypePointer(v.storage, st), v.ptr, idx)
	case v.typ.Main == shaderir.Mat2 || v.typ.Main == shaderir.Mat3 || v.typ.Main == shaderir.Mat4: // m[i] = column
		et = shaderir.Type{Main: shaderir.Vec2 + (v.typ.Main - shaderir.Mat2)}
		st, err := c.typ(et)
		if err != nil {
			return err
		}
		var idx uint32
		if ix := lhs.Exprs[1]; ix.Type == shaderir.NumberExpr {
			n, _ := constant.Int64Val(constant.ToInt(ix.Const))
			idx = c.m.ConstantInt32(int32(n))
		} else {
			i, err := c.expr(f, ix)
			if err != nil {
				return err
			}
			idx = i.id
		}
		ptr = f.AccessChain(c.m.TypePointer(v.storage, st), v.ptr, idx)
	default:
		return fmt.Errorf("spirv: assignment to an index of a %s not supported yet", v.typ.String())
	}
	var rhs value
	if rhsExpr.Type == shaderir.NumberExpr && (et.Main == shaderir.Int || et.Main == shaderir.Float) {
		id, err := c.forConst(et, rhsExpr.Const)
		if err != nil {
			return err
		}
		rhs = value{id, et}
	} else if rhs, err = c.expr(f, rhsExpr); err != nil {
		return err
	}
	f.Store(ptr, rhs.id)
	return nil
}

// copyAssignedInputs gives each stage input the body assigns (dstPos -= …,
// src0Pos.x = 0) a Function variable holding its value — Input variables
// can't be written — and reads and writes it there from then on. The
// variables are made first (they must open the block), then filled.
func (c *compiler) copyAssignedInputs(f *Function, block *shaderir.Block) error {
	assigned := map[int]bool{}
	var walk func(b *shaderir.Block)
	walk = func(b *shaderir.Block) {
		for _, s := range b.Stmts {
			if s.Type == shaderir.Assign && len(s.Exprs) > 0 {
				lhs := s.Exprs[0]
				for lhs.Type == shaderir.FieldSelector || lhs.Type == shaderir.Index {
					lhs = lhs.Exprs[0]
				}
				if lhs.Type == shaderir.LocalVariable {
					if v, ok := c.vars[lhs.Index]; ok && v.input {
						assigned[lhs.Index] = true
					}
				}
			}
			for _, bb := range s.Blocks {
				if bb != nil {
					walk(bb)
				}
			}
		}
	}
	walk(block)
	type copyOf struct {
		index int
		in    variable
		ptr   uint32
		st    uint32
	}
	var copies []copyOf
	for i := range assigned {
		in := c.vars[i]
		st, err := c.typ(in.typ)
		if err != nil {
			return err
		}
		copies = append(copies, copyOf{i, in, f.LocalVariable(c.m.TypePointer(StorageClassFunction, st)), st})
	}
	for _, cp := range copies {
		f.Store(cp.ptr, f.Load(cp.st, cp.in.ptr))
		c.vars[cp.index] = variable{ptr: cp.ptr, typ: cp.in.typ, storage: StorageClassFunction}
	}
	return nil
}

// zeroOnEntry records a variable of block b to be zeroed when b is entered.
func (c *compiler) zeroOnEntry(b *shaderir.Block, ptr, typ uint32) {
	if c.zeroes == nil {
		c.zeroes = map[*shaderir.Block][][2]uint32{}
	}
	c.zeroes[b] = append(c.zeroes[b], [2]uint32{ptr, typ})
}
