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

package vk

import (
	"runtime"
	"unsafe"

	"github.com/ebitengine/purego"
)

// StructureTypeShaderModuleCreateInfo is VK_STRUCTURE_TYPE_SHADER_MODULE_CREATE_INFO.
const StructureTypeShaderModuleCreateInfo StructureType = 16

// ShaderModule is a non-dispatchable handle.
type ShaderModule uint64

// ShaderModuleCreateInfo is VkShaderModuleCreateInfo.
type ShaderModuleCreateInfo struct {
	SType    StructureType
	PNext    unsafe.Pointer
	Flags    uint32
	CodeSize uintptr // in bytes
	PCode    *uint32
}

// CreateShaderModule is vkCreateShaderModule of SPIR-V words.
func (d *Device) CreateShaderModule(code []uint32) (ShaderModule, error) {
	if len(code) == 0 {
		return 0, ErrorInitializationFailed
	}
	var pinner runtime.Pinner
	defer pinner.Unpin()
	pinner.Pin(&code[0])
	info := &ShaderModuleCreateInfo{SType: StructureTypeShaderModuleCreateInfo, CodeSize: uintptr(len(code) * 4), PCode: &code[0]}
	pinner.Pin(info)
	var sm ShaderModule
	r, _, _ := purego.SyscallN(d.fn.createShaderModule, d.handle, uintptr(unsafe.Pointer(info)), 0, uintptr(unsafe.Pointer(&sm)))
	return sm, result(r)
}

// DestroyShaderModule is vkDestroyShaderModule.
func (d *Device) DestroyShaderModule(sm ShaderModule) {
	purego.SyscallN(d.fn.destroyShaderModule, d.handle, uintptr(sm), 0)
}
