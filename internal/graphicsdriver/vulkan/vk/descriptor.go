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

// Descriptor handles.
type (
	DescriptorSetLayout uint64
	DescriptorPool      uint64
	DescriptorSet       uint64
)

// Structure types of descriptors.
const (
	StructureTypeDescriptorSetLayoutCreateInfo StructureType = 32
	StructureTypeDescriptorPoolCreateInfo      StructureType = 33
	StructureTypeDescriptorSetAllocateInfo     StructureType = 34
	StructureTypeWriteDescriptorSet            StructureType = 35
)

// DescriptorTypeUniformBufferDynamic: a uniform buffer whose offset is given
// when the set is bound (one buffer, a slice per draw).
const DescriptorTypeUniformBufferDynamic uint32 = 8

// DescriptorSetLayoutBinding is VkDescriptorSetLayoutBinding.
type DescriptorSetLayoutBinding struct {
	Binding            uint32
	DescriptorType     uint32
	DescriptorCount    uint32
	StageFlags         uint32
	PImmutableSamplers uintptr
}

// DescriptorSetLayoutCreateInfo is VkDescriptorSetLayoutCreateInfo.
type DescriptorSetLayoutCreateInfo struct {
	SType        StructureType
	PNext        unsafe.Pointer
	Flags        uint32
	BindingCount uint32
	PBindings    *DescriptorSetLayoutBinding
}

// DescriptorPoolSize is VkDescriptorPoolSize.
type DescriptorPoolSize struct {
	Type            uint32
	DescriptorCount uint32
}

// DescriptorPoolCreateInfo is VkDescriptorPoolCreateInfo.
type DescriptorPoolCreateInfo struct {
	SType         StructureType
	PNext         unsafe.Pointer
	Flags         uint32
	MaxSets       uint32
	PoolSizeCount uint32
	PPoolSizes    *DescriptorPoolSize
}

// DescriptorSetAllocateInfo is VkDescriptorSetAllocateInfo.
type DescriptorSetAllocateInfo struct {
	SType              StructureType
	PNext              unsafe.Pointer
	DescriptorPool     DescriptorPool
	DescriptorSetCount uint32
	PSetLayouts        *DescriptorSetLayout
}

// DescriptorBufferInfo is VkDescriptorBufferInfo.
type DescriptorBufferInfo struct {
	Buffer Buffer
	Offset uint64
	Range  uint64
}

// WriteDescriptorSet is VkWriteDescriptorSet.
type WriteDescriptorSet struct {
	SType            StructureType
	PNext            unsafe.Pointer
	DstSet           DescriptorSet
	DstBinding       uint32
	DstArrayElement  uint32
	DescriptorCount  uint32
	DescriptorType   uint32
	PImageInfo       uintptr
	PBufferInfo      *DescriptorBufferInfo
	PTexelBufferView uintptr
}

// CreateDescriptorSetLayout is vkCreateDescriptorSetLayout of the bindings.
func (d *Device) CreateDescriptorSetLayout(bindings ...DescriptorSetLayoutBinding) (DescriptorSetLayout, error) {
	var pinner runtime.Pinner
	defer pinner.Unpin()
	info := &DescriptorSetLayoutCreateInfo{SType: StructureTypeDescriptorSetLayoutCreateInfo, BindingCount: uint32(len(bindings))}
	if len(bindings) > 0 {
		pinner.Pin(&bindings[0])
		info.PBindings = &bindings[0]
	}
	pinner.Pin(info)
	var l DescriptorSetLayout
	r, _, _ := purego.SyscallN(d.fn.createDescriptorSetLayout, d.handle, uintptr(unsafe.Pointer(info)), 0, uintptr(unsafe.Pointer(&l)))
	return l, result(r)
}

// DestroyDescriptorSetLayout is vkDestroyDescriptorSetLayout.
func (d *Device) DestroyDescriptorSetLayout(l DescriptorSetLayout) {
	purego.SyscallN(d.fn.destroyDescriptorSetLayout, d.handle, uintptr(l), 0)
}

// CreateDescriptorPool is vkCreateDescriptorPool for up to maxSets sets
// holding the given descriptors.
func (d *Device) CreateDescriptorPool(maxSets uint32, sizes ...DescriptorPoolSize) (DescriptorPool, error) {
	var pinner runtime.Pinner
	defer pinner.Unpin()
	info := &DescriptorPoolCreateInfo{SType: StructureTypeDescriptorPoolCreateInfo, MaxSets: maxSets, PoolSizeCount: uint32(len(sizes))}
	if len(sizes) > 0 {
		pinner.Pin(&sizes[0])
		info.PPoolSizes = &sizes[0]
	}
	pinner.Pin(info)
	var p DescriptorPool
	r, _, _ := purego.SyscallN(d.fn.createDescriptorPool, d.handle, uintptr(unsafe.Pointer(info)), 0, uintptr(unsafe.Pointer(&p)))
	return p, result(r)
}

// DestroyDescriptorPool is vkDestroyDescriptorPool (its sets go with it).
func (d *Device) DestroyDescriptorPool(p DescriptorPool) {
	purego.SyscallN(d.fn.destroyDescriptorPool, d.handle, uintptr(p), 0)
}

// AllocateDescriptorSet is vkAllocateDescriptorSets of one set of a layout.
func (d *Device) AllocateDescriptorSet(pool DescriptorPool, layout DescriptorSetLayout) (DescriptorSet, error) {
	var pinner runtime.Pinner
	defer pinner.Unpin()
	pinner.Pin(&layout)
	info := &DescriptorSetAllocateInfo{SType: StructureTypeDescriptorSetAllocateInfo, DescriptorPool: pool, DescriptorSetCount: 1, PSetLayouts: &layout}
	pinner.Pin(info)
	var s DescriptorSet
	r, _, _ := purego.SyscallN(d.fn.allocateDescriptorSets, d.handle, uintptr(unsafe.Pointer(info)), uintptr(unsafe.Pointer(&s)))
	return s, result(r)
}

// WriteUniformBuffer is vkUpdateDescriptorSets writing one dynamic uniform
// buffer (rangeSize bytes from offset; the bind adds its dynamic offset) at
// a binding of a set.
func (d *Device) WriteUniformBuffer(set DescriptorSet, binding uint32, b Buffer, offset, rangeSize uint64) {
	var pinner runtime.Pinner
	defer pinner.Unpin()
	buf := &DescriptorBufferInfo{Buffer: b, Offset: offset, Range: rangeSize}
	pinner.Pin(buf)
	w := &WriteDescriptorSet{SType: StructureTypeWriteDescriptorSet, DstSet: set, DstBinding: binding, DescriptorCount: 1,
		DescriptorType: DescriptorTypeUniformBufferDynamic, PBufferInfo: buf}
	pinner.Pin(w)
	purego.SyscallN(d.fn.updateDescriptorSets, d.handle, 1, uintptr(unsafe.Pointer(w)), 0, 0)
}

// CmdBindDescriptorSet is vkCmdBindDescriptorSets of one set (set 0) at the
// graphics bind point, with its dynamic offsets.
func (d *Device) CmdBindDescriptorSet(cb CommandBuffer, layout PipelineLayout, set DescriptorSet, dynamicOffsets ...uint32) {
	var offs uintptr
	if len(dynamicOffsets) > 0 {
		offs = uintptr(unsafe.Pointer(&dynamicOffsets[0]))
	}
	purego.SyscallN(d.fn.cmdBindDescriptorSets, uintptr(cb), uintptr(PipelineBindPointGraphics), uintptr(layout), 0, 1,
		uintptr(unsafe.Pointer(&set)), uintptr(len(dynamicOffsets)), offs)
	runtime.KeepAlive(&set)
	runtime.KeepAlive(dynamicOffsets)
}
