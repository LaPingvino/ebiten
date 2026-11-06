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

// Package spirv writes SPIR-V modules — the shader binaries Vulkan takes —
// from Kage's shader IR. It writes the binary itself (no external compiler),
// section by section in the order the SPIR-V specification's logical layout
// requires (§2.4), and has nothing of Vulkan in it, so it is tested on its own:
// every module its tests write passes spirv-val where SPIRV-Tools is installed.
package spirv

import (
	"encoding/binary"
	"math"
	"slices"
	"strconv"
)

// The header (§2.3).
const (
	magic   = 0x07230203
	version = 0x00010600 // SPIR-V 1.6, the version Vulkan 1.3 takes
)

// Opcodes (§3.52) the module uses.
const (
	opName                   = 5
	opMemoryModel            = 14
	opEntryPoint             = 15
	opExecutionMode          = 16
	opCapability             = 17
	opTypeVoid               = 19
	opTypeBool               = 20
	opTypeInt                = 21
	opTypeFloat              = 22
	opTypeVector             = 23
	opTypeMatrix             = 24
	opTypeArray              = 28
	opTypeImage              = 25
	opTypeSampledImage       = 27
	opVectorShuffle          = 79
	opConstantNull           = 46
	opConvertFToS            = 110
	opLoopMerge              = 246
	opUnreachable            = 255
	opSelectionMerge         = 247
	opBranch                 = 249
	opBranchConditional      = 250
	opPhi                    = 245
	opLogicalOr              = 166
	opLogicalAnd             = 167
	opLogicalNot             = 168
	opIEqual                 = 170
	opINotEqual              = 171
	opSGreaterThan           = 173
	opSGreaterThanEqual      = 175
	opSLessThan              = 177
	opSLessThanEqual         = 179
	opFOrdEqual              = 180
	opFUnordNotEqual         = 183
	opFOrdLessThan           = 184
	opFOrdGreaterThan        = 186
	opFOrdLessThanEqual      = 188
	opFOrdGreaterThanEqual   = 190
	opFNegate                = 127
	opSNegate                = 126
	opConstantTrue           = 41
	opConstantFalse          = 42
	opDPdx                   = 207
	opDPdy                   = 208
	opFwidth                 = 209
	opFMod                   = 141
	opDot                    = 148
	opExtInstImport          = 11
	opExtInst                = 12
	opImageSampleExplicitLod = 88
	opTypeStruct             = 30
	opTypePointer            = 32
	opConstant               = 43
	opConstantComposite      = 44
	opVariable               = 59
	opLoad                   = 61
	opStore                  = 62
	opDecorate               = 71
	opMemberDecorate         = 72
	opAccessChain            = 65
	opMatrixTimesVector      = 145
	opTypeFunction           = 33
	opFunction               = 54
	opFunctionEnd            = 56
	opCompositeConstruct     = 80
	opCompositeExtract       = 81
	opConvertSToF            = 111
	opFSub                   = 131
	opFunctionParameter      = 55
	opFunctionCall           = 57
	opReturnValue            = 254
	opIAdd                   = 128
	opFAdd                   = 129
	opISub                   = 130
	opIMul                   = 132
	opSDiv                   = 135
	opFDiv                   = 136
	opVectorTimesScalar      = 142
	opFMul                   = 133
	opShiftLeftLogical       = 196
	opBitwiseAnd             = 199
	opLabel                  = 248
	opReturn                 = 253
)

// Operand values.
const (
	CapabilityShader             = 1
	AddressingLogical            = 0
	MemoryModelGLSL450           = 1
	ExecutionModelVertex         = 0
	ExecutionModelFragment       = 4
	ExecutionModeOriginUpperLeft = 7
	FunctionControlNone          = 0

	StorageClassUniformConstant = 0
	StorageClassInput           = 1
	StorageClassUniform         = 2
	StorageClassOutput          = 3
	StorageClassFunction        = 7

	DecorationBlock         = 2
	DecorationColMajor      = 5
	DecorationArrayStride   = 6
	DecorationMatrixStride  = 7
	DecorationBuiltIn       = 11
	DecorationLocation      = 30
	DecorationBinding       = 33
	DecorationDescriptorSet = 34
	DecorationOffset        = 35

	BuiltInPosition    = 0
	BuiltInFragCoord   = 15
	BuiltInVertexIndex = 42
)

// Module is a SPIR-V module being written. IDs come from one counter; each
// section collects its instructions, joined in layout order by Words.
type Module struct {
	bound uint32 // the next ID

	capabilities, extInstImports, memoryModel, entryPoints []uint32
	executionModes                                         []uint32
	debug, annotations, types, functions                   []uint32

	typeIDs map[string]uint32 // types written once
	glsl    uint32            // the GLSL.std.450 import (0: not yet)
}

// NewModule is an empty module.
func NewModule() *Module {
	return &Module{bound: 1, typeIDs: map[string]uint32{}}
}

// ID is a new result ID.
func (m *Module) ID() uint32 {
	id := m.bound
	m.bound++
	return id
}

// inst appends one instruction: its word count and opcode, then its operands.
func inst(section *[]uint32, op uint16, operands ...uint32) {
	*section = append(*section, uint32(len(operands)+1)<<16|uint32(op))
	*section = append(*section, operands...)
}

// str is a literal string's words: UTF-8, NUL-terminated, padded with NULs to
// a whole word (§2.2.1).
func str(s string) []uint32 {
	b := append([]byte(s), 0)
	for len(b)%4 != 0 {
		b = append(b, 0)
	}
	w := make([]uint32, len(b)/4)
	for i := range w {
		w[i] = binary.LittleEndian.Uint32(b[i*4:])
	}
	return w
}

// Capability declares a capability.
func (m *Module) Capability(c uint32) { inst(&m.capabilities, opCapability, c) }

// MemoryModel sets the addressing and memory model (one per module).
func (m *Module) MemoryModel(addressing, memory uint32) {
	m.memoryModel = m.memoryModel[:0]
	inst(&m.memoryModel, opMemoryModel, addressing, memory)
}

// EntryPoint declares fn as an entry point of the execution model, with the
// global variables its interface uses.
func (m *Module) EntryPoint(model, fn uint32, name string, interfaces ...uint32) {
	ops := append([]uint32{model, fn}, str(name)...)
	inst(&m.entryPoints, opEntryPoint, append(ops, interfaces...)...)
}

// ExecutionMode sets an execution mode of an entry point.
func (m *Module) ExecutionMode(fn, mode uint32, literals ...uint32) {
	inst(&m.executionModes, opExecutionMode, append([]uint32{fn, mode}, literals...)...)
}

// Name gives an ID a debug name (what spirv-dis and drivers' errors show).
func (m *Module) Name(id uint32, name string) {
	inst(&m.debug, opName, append([]uint32{id}, str(name)...)...)
}

// typeID is a type's ID, written the first time it is asked for.
func (m *Module) typeID(key string, write func(id uint32)) uint32 {
	if id, ok := m.typeIDs[key]; ok {
		return id
	}
	id := m.ID()
	m.typeIDs[key] = id
	write(id)
	return id
}

// TypeVoid is OpTypeVoid.
func (m *Module) TypeVoid() uint32 {
	return m.typeID("void", func(id uint32) { inst(&m.types, opTypeVoid, id) })
}

// TypeFunction is OpTypeFunction of a return type and parameter types.
func (m *Module) TypeFunction(ret uint32, params ...uint32) uint32 {
	return m.typeID(key("fn", append([]uint32{ret}, params...)...), func(id uint32) {
		inst(&m.types, opTypeFunction, append([]uint32{id, ret}, params...)...)
	})
}

// key is a cache key of words.
func key(kind string, words ...uint32) string {
	for _, w := range words {
		kind += " " + strconv.FormatUint(uint64(w), 10)
	}
	return kind
}

// TypeBool is OpTypeBool.
func (m *Module) TypeBool() uint32 {
	return m.typeID("bool", func(id uint32) { inst(&m.types, opTypeBool, id) })
}

// TypeInt is OpTypeInt of a width (32), signed or not.
func (m *Module) TypeInt(width uint32, signed bool) uint32 {
	s := uint32(0)
	if signed {
		s = 1
	}
	return m.typeID(key("int", width, s), func(id uint32) { inst(&m.types, opTypeInt, id, width, s) })
}

// TypeFloat is OpTypeFloat of a width (32).
func (m *Module) TypeFloat(width uint32) uint32 {
	return m.typeID(key("float", width), func(id uint32) { inst(&m.types, opTypeFloat, id, width) })
}

// TypeVector is OpTypeVector of n components.
func (m *Module) TypeVector(component, n uint32) uint32 {
	return m.typeID(key("vec", component, n), func(id uint32) { inst(&m.types, opTypeVector, id, component, n) })
}

// TypeMatrix is OpTypeMatrix of n column vectors.
func (m *Module) TypeMatrix(column, n uint32) uint32 {
	return m.typeID(key("mat", column, n), func(id uint32) { inst(&m.types, opTypeMatrix, id, column, n) })
}

// TypeImage2D is OpTypeImage of a sampled 2D float image (not depth,
// arrayed or multisampled; format unknown) — what a texture is.
func (m *Module) TypeImage2D() uint32 {
	f := m.TypeFloat(32)
	return m.typeID(key("img2d", f), func(id uint32) { inst(&m.types, opTypeImage, id, f, 1, 0, 0, 0, 1, 0) })
}

// TypeSampledImage is OpTypeSampledImage of an image type (a combined image
// sampler's type).
func (m *Module) TypeSampledImage(image uint32) uint32 {
	return m.typeID(key("simg", image), func(id uint32) { inst(&m.types, opTypeSampledImage, id, image) })
}

// TypeArray is OpTypeArray of n elements (its length an unsigned constant).
// A new type per call: its ArrayStride decoration is its own.
func (m *Module) TypeArray(element uint32, n int) uint32 {
	length := m.ConstantUint32(uint32(n))
	id := m.ID()
	inst(&m.types, opTypeArray, id, element, length)
	return id
}

// TypeStruct is OpTypeStruct of the members — a new type each call, since
// its decorations (Block, member offsets) belong to that one.
func (m *Module) TypeStruct(members ...uint32) uint32 {
	id := m.ID()
	inst(&m.types, opTypeStruct, append([]uint32{id}, members...)...)
	return id
}

// TypePointer is OpTypePointer to a type in a storage class.
func (m *Module) TypePointer(storage, typ uint32) uint32 {
	return m.typeID(key("ptr", storage, typ), func(id uint32) { inst(&m.types, opTypePointer, id, storage, typ) })
}

// ConstantFloat32 is a 32-bit float constant (written once per value).
func (m *Module) ConstantFloat32(v float32) uint32 {
	f := m.TypeFloat(32)
	bits := math.Float32bits(v)
	return m.typeID(key("cf", f, bits), func(id uint32) { inst(&m.types, opConstant, f, id, bits) })
}

// ConstantInt32 is a 32-bit signed integer constant (written once per value).
func (m *Module) ConstantInt32(v int32) uint32 {
	i := m.TypeInt(32, true)
	bits := uint32(v)
	return m.typeID(key("ci", i, bits), func(id uint32) { inst(&m.types, opConstant, i, id, bits) })
}

// ConstantUint32 is a 32-bit unsigned integer constant (written once per value).
func (m *Module) ConstantUint32(v uint32) uint32 {
	u := m.TypeInt(32, false)
	return m.typeID(key("cu", u, v), func(id uint32) { inst(&m.types, opConstant, u, id, v) })
}

// ConstantComposite is a composite constant (a vector of constants).
func (m *Module) ConstantComposite(typ uint32, parts ...uint32) uint32 {
	return m.typeID(key("cc", append([]uint32{typ}, parts...)...), func(id uint32) {
		inst(&m.types, opConstantComposite, append([]uint32{typ, id}, parts...)...)
	})
}

// Variable is a global OpVariable of a pointer type in a storage class (not
// Function: those go in a function's first block).
func (m *Module) Variable(pointerType, storage uint32) uint32 {
	id := m.ID()
	inst(&m.types, opVariable, pointerType, id, storage)
	return id
}

// Decorate decorates an ID (Location 0, BuiltIn Position …).
func (m *Module) Decorate(id, decoration uint32, literals ...uint32) {
	inst(&m.annotations, opDecorate, append([]uint32{id, decoration}, literals...)...)
}

// MemberDecorate decorates a struct's member (Offset 0, ColMajor …).
func (m *Module) MemberDecorate(structType, member, decoration uint32, literals ...uint32) {
	inst(&m.annotations, opMemberDecorate, append([]uint32{structType, member, decoration}, literals...)...)
}

// Function begins a function of a function type returning ret; body writes
// its blocks (each from a Label), and the function is ended after it.
func (m *Module) Function(ret, fnType uint32, body func(f *Function)) uint32 {
	id := m.ID()
	inst(&m.functions, opFunction, ret, id, FunctionControlNone, fnType)
	body(&Function{m: m})
	inst(&m.functions, opFunctionEnd)
	return id
}

// Function writes a function's body.
type Function struct {
	m       *Module
	current uint32 // the label of the block being written
}

// Label begins a block.
func (f *Function) Label() uint32 {
	id := f.m.ID()
	f.LabelAt(id)
	return id
}

// LabelAt begins the block of a label made earlier (by Module.ID: a branch
// target named before its block is written).
func (f *Function) LabelAt(id uint32) {
	inst(&f.m.functions, opLabel, id)
	f.current = id
}

// Current is the label of the block being written (for OpPhi).
func (f *Function) Current() uint32 { return f.current }

// SelectionMerge declares a structured selection's merge block (before its
// conditional branch).
func (f *Function) SelectionMerge(merge uint32) { inst(&f.m.functions, opSelectionMerge, merge, 0) }

// LoopMerge declares a structured loop's merge and continue blocks (in its
// header, before the branch).
func (f *Function) LoopMerge(merge, cont uint32) { inst(&f.m.functions, opLoopMerge, merge, cont, 0) }

// Unreachable ends a block control never reaches.
func (f *Function) Unreachable() { inst(&f.m.functions, opUnreachable) }

// Branch ends a block jumping to a label.
func (f *Function) Branch(to uint32) { inst(&f.m.functions, opBranch, to) }

// BranchConditional ends a block jumping on a bool.
func (f *Function) BranchConditional(cond, ifTrue, ifFalse uint32) {
	inst(&f.m.functions, opBranchConditional, cond, ifTrue, ifFalse)
}

// Phi is a value by the block control came from: pairs of (value, label).
func (f *Function) Phi(typ uint32, pairs ...uint32) uint32 { return f.op(opPhi, typ, pairs...) }

// ConstantNull is the zero value of a type (OpConstantNull).
func (m *Module) ConstantNull(typ uint32) uint32 {
	return m.typeID(key("cnull", typ), func(id uint32) { inst(&m.types, opConstantNull, typ, id) })
}

// ConstantBool is true or false.
func (m *Module) ConstantBool(v bool) uint32 {
	b := m.TypeBool()
	op, k := uint16(opConstantFalse), uint32(0)
	if v {
		op, k = opConstantTrue, 1
	}
	return m.typeID(key("cb", b, k), func(id uint32) { inst(&m.types, op, b, id) })
}

// LocalVariable is a Function-storage OpVariable — only at the start of a
// function's first block, right after its Label.
func (f *Function) LocalVariable(pointerType uint32) uint32 {
	id := f.m.ID()
	inst(&f.m.functions, opVariable, pointerType, id, StorageClassFunction)
	return id
}

// Store is OpStore of an object through a pointer.
func (f *Function) Store(pointer, object uint32) { inst(&f.m.functions, opStore, pointer, object) }

// Load is OpLoad of a type through a pointer.
func (f *Function) Load(typ, pointer uint32) uint32 {
	id := f.m.ID()
	inst(&f.m.functions, opLoad, typ, id, pointer)
	return id
}

// op writes an instruction with a result of a type, returning the result.
func (f *Function) op(op uint16, typ uint32, operands ...uint32) uint32 {
	id := f.m.ID()
	inst(&f.m.functions, op, append([]uint32{typ, id}, operands...)...)
	return id
}

// ShiftLeftLogical is a << shift (integers).
func (f *Function) ShiftLeftLogical(typ, a, shift uint32) uint32 {
	return f.op(opShiftLeftLogical, typ, a, shift)
}

// BitwiseAnd is a & b (integers).
func (f *Function) BitwiseAnd(typ, a, b uint32) uint32 { return f.op(opBitwiseAnd, typ, a, b) }

// ConvertSToF is a signed integer as a float.
func (f *Function) ConvertSToF(typ, a uint32) uint32 { return f.op(opConvertSToF, typ, a) }

// FMul is a * b; FSub is a - b (floats or float vectors).
func (f *Function) FMul(typ, a, b uint32) uint32 { return f.op(opFMul, typ, a, b) }
func (f *Function) FSub(typ, a, b uint32) uint32 { return f.op(opFSub, typ, a, b) }

// CompositeConstruct builds a composite (a vector) from its parts.
func (f *Function) CompositeConstruct(typ uint32, parts ...uint32) uint32 {
	return f.op(opCompositeConstruct, typ, parts...)
}

// CompositeExtract is one part of a composite (a vector's component).
func (f *Function) CompositeExtract(typ, composite uint32, indices ...uint32) uint32 {
	return f.op(opCompositeExtract, typ, append([]uint32{composite}, indices...)...)
}

// AccessChain is a pointer into a composite (a struct's member: indices are
// constant IDs).
func (f *Function) AccessChain(pointerType, base uint32, indices ...uint32) uint32 {
	return f.op(opAccessChain, pointerType, append([]uint32{base}, indices...)...)
}

// MatrixTimesVector is matrix × vector (columns-major, as GLSL's m * v).
func (f *Function) MatrixTimesVector(typ, matrix, vector uint32) uint32 {
	return f.op(opMatrixTimesVector, typ, matrix, vector)
}

// Parameter is an OpFunctionParameter of a type — only before the
// function's first Label, one per parameter in the function type's order.
func (f *Function) Parameter(typ uint32) uint32 {
	id := f.m.ID()
	inst(&f.m.functions, opFunctionParameter, typ, id)
	return id
}

// Call is OpFunctionCall of a function returning typ with the arguments.
func (f *Function) Call(typ, fn uint32, args ...uint32) uint32 {
	return f.op(opFunctionCall, typ, append([]uint32{fn}, args...)...)
}

// ReturnValue ends a block returning a value.
func (f *Function) ReturnValue(v uint32) { inst(&f.m.functions, opReturnValue, v) }

// Arithmetic: a op b of one type (floats or float vectors: F*; integers: I*,
// SDiv); VectorTimesScalar is a float vector × a float.
func (f *Function) FAdd(typ, a, b uint32) uint32 { return f.op(opFAdd, typ, a, b) }
func (f *Function) FDiv(typ, a, b uint32) uint32 { return f.op(opFDiv, typ, a, b) }
func (f *Function) IAdd(typ, a, b uint32) uint32 { return f.op(opIAdd, typ, a, b) }
func (f *Function) ISub(typ, a, b uint32) uint32 { return f.op(opISub, typ, a, b) }
func (f *Function) IMul(typ, a, b uint32) uint32 { return f.op(opIMul, typ, a, b) }
func (f *Function) SDiv(typ, a, b uint32) uint32 { return f.op(opSDiv, typ, a, b) }
func (f *Function) VectorTimesScalar(typ, v, s uint32) uint32 {
	return f.op(opVectorTimesScalar, typ, v, s)
}

// VectorShuffle picks components of two vectors (indices into a ++ b).
func (f *Function) VectorShuffle(typ, a, b uint32, components ...uint32) uint32 {
	return f.op(opVectorShuffle, typ, append([]uint32{a, b}, components...)...)
}

// ImageSampleExplicitLod samples a sampled image at coordinates with an
// explicit level of detail (ImageOperands Lod) — the only way with
// unnormalized coordinates.
func (f *Function) ImageSampleExplicitLod(typ, sampledImage, coord, lod uint32) uint32 {
	return f.op(opImageSampleExplicitLod, typ, sampledImage, coord, 0x2, lod)
}

// GLSL.std.450 instruction numbers (the extended instructions used).
const (
	GLSLFloor = 8
)

// GLSLStd450 is the GLSL.std.450 extended instruction set's import, made on
// first use.
func (m *Module) GLSLStd450() uint32 {
	if m.glsl == 0 {
		m.glsl = m.ID()
		inst(&m.extInstImports, opExtInstImport, append([]uint32{m.glsl}, str("GLSL.std.450")...)...)
	}
	return m.glsl
}

// GLSL is an OpExtInst of GLSL.std.450 (GLSLFloor …) on the operands.
func (f *Function) GLSL(typ, instruction uint32, operands ...uint32) uint32 {
	return f.op(opExtInst, typ, append([]uint32{f.m.GLSLStd450(), instruction}, operands...)...)
}

// Return ends a block, returning nothing.
func (f *Function) Return() { inst(&f.m.functions, opReturn) }

// Words is the module: the header, then the sections in layout order.
func (m *Module) Words() []uint32 {
	return slices.Concat([]uint32{magic, version, 0, m.bound, 0},
		m.capabilities, m.extInstImports, m.memoryModel, m.entryPoints, m.executionModes, m.debug, m.annotations, m.types, m.functions)
}

// Bytes is the module as little-endian bytes (what a .spv file and
// VkShaderModuleCreateInfo hold).
func (m *Module) Bytes() []byte {
	w := m.Words()
	b := make([]byte, len(w)*4)
	for i, x := range w {
		binary.LittleEndian.PutUint32(b[i*4:], x)
	}
	return b
}
