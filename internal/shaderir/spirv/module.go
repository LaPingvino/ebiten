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
	opName          = 5
	opMemoryModel   = 14
	opEntryPoint    = 15
	opExecutionMode = 16
	opCapability    = 17
	opTypeVoid      = 19
	opTypeFunction  = 33
	opFunction      = 54
	opFunctionEnd   = 56
	opLabel         = 248
	opReturn        = 253
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
)

// Module is a SPIR-V module being written. IDs come from one counter; each
// section collects its instructions, joined in layout order by Words.
type Module struct {
	bound uint32 // the next ID

	capabilities, memoryModel, entryPoints, executionModes []uint32
	debug, types, functions                                []uint32

	typeIDs map[string]uint32 // types written once
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
	key := "fn"
	for _, p := range append([]uint32{ret}, params...) {
		key += " " + strconv.FormatUint(uint64(p), 10)
	}
	return m.typeID(key, func(id uint32) {
		inst(&m.types, opTypeFunction, append([]uint32{id, ret}, params...)...)
	})
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
type Function struct{ m *Module }

// Label begins a block.
func (f *Function) Label() uint32 {
	id := f.m.ID()
	inst(&f.m.functions, opLabel, id)
	return id
}

// Return ends a block, returning nothing.
func (f *Function) Return() { inst(&f.m.functions, opReturn) }

// Words is the module: the header, then the sections in layout order.
func (m *Module) Words() []uint32 {
	return slices.Concat([]uint32{magic, version, 0, m.bound, 0},
		m.capabilities, m.memoryModel, m.entryPoints, m.executionModes, m.debug, m.types, m.functions)
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
