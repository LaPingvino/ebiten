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

// Structure types used by the command functions.
const (
	StructureTypeSubmitInfo                StructureType = 4
	StructureTypeCommandPoolCreateInfo     StructureType = 39
	StructureTypeCommandBufferAllocateInfo StructureType = 40
	StructureTypeCommandBufferBeginInfo    StructureType = 42
)

// CommandPool is a non-dispatchable handle; CommandBuffer a dispatchable one.
type (
	CommandPool   uint64
	CommandBuffer uintptr
)

// Command pool and buffer flags.
const (
	CommandPoolCreateResetCommandBufferBit uint32 = 0x2
	CommandBufferUsageOneTimeSubmitBit     uint32 = 0x1
)

// CommandPoolCreateInfo is VkCommandPoolCreateInfo.
type CommandPoolCreateInfo struct {
	SType            StructureType
	PNext            unsafe.Pointer
	Flags            uint32
	QueueFamilyIndex uint32
}

// CommandBufferAllocateInfo is VkCommandBufferAllocateInfo.
type CommandBufferAllocateInfo struct {
	SType              StructureType
	PNext              unsafe.Pointer
	CommandPool        CommandPool
	Level              uint32
	CommandBufferCount uint32
}

// CommandBufferBeginInfo is VkCommandBufferBeginInfo.
type CommandBufferBeginInfo struct {
	SType            StructureType
	PNext            unsafe.Pointer
	Flags            uint32
	PInheritanceInfo uintptr
}

// SubmitInfo is VkSubmitInfo.
type SubmitInfo struct {
	SType                StructureType
	PNext                unsafe.Pointer
	WaitSemaphoreCount   uint32
	PWaitSemaphores      uintptr
	PWaitDstStageMask    uintptr
	CommandBufferCount   uint32
	PCommandBuffers      *CommandBuffer
	SignalSemaphoreCount uint32
	PSignalSemaphores    uintptr
}

// CreateCommandPool is vkCreateCommandPool for a queue family.
func (d *Device) CreateCommandPool(queueFamily uint32, flags uint32) (CommandPool, error) {
	info := &CommandPoolCreateInfo{SType: StructureTypeCommandPoolCreateInfo, Flags: flags, QueueFamilyIndex: queueFamily}
	var p CommandPool
	r, _, _ := purego.SyscallN(d.fn.createCommandPool, d.handle, uintptr(unsafe.Pointer(info)), 0, uintptr(unsafe.Pointer(&p)))
	runtime.KeepAlive(info)
	return p, result(r)
}

// DestroyCommandPool is vkDestroyCommandPool (its command buffers go with it).
func (d *Device) DestroyCommandPool(p CommandPool) {
	purego.SyscallN(d.fn.destroyCommandPool, d.handle, uintptr(p), 0)
}

// AllocateCommandBuffer is vkAllocateCommandBuffers for one primary buffer.
func (d *Device) AllocateCommandBuffer(p CommandPool) (CommandBuffer, error) {
	info := &CommandBufferAllocateInfo{SType: StructureTypeCommandBufferAllocateInfo, CommandPool: p, CommandBufferCount: 1}
	var cb CommandBuffer
	r, _, _ := purego.SyscallN(d.fn.allocateCommandBuffers, d.handle, uintptr(unsafe.Pointer(info)), uintptr(unsafe.Pointer(&cb)))
	runtime.KeepAlive(info)
	return cb, result(r)
}

// BeginCommandBuffer is vkBeginCommandBuffer.
func (d *Device) BeginCommandBuffer(cb CommandBuffer, flags uint32) error {
	info := &CommandBufferBeginInfo{SType: StructureTypeCommandBufferBeginInfo, Flags: flags}
	r, _, _ := purego.SyscallN(d.fn.beginCommandBuffer, uintptr(cb), uintptr(unsafe.Pointer(info)))
	runtime.KeepAlive(info)
	return result(r)
}

// EndCommandBuffer is vkEndCommandBuffer.
func (d *Device) EndCommandBuffer(cb CommandBuffer) error {
	r, _, _ := purego.SyscallN(d.fn.endCommandBuffer, uintptr(cb))
	return result(r)
}

// CmdFillBuffer is vkCmdFillBuffer: size bytes from offset set to the
// repeated 32-bit word data (size a multiple of 4).
func (d *Device) CmdFillBuffer(cb CommandBuffer, b Buffer, offset, size uint64, data uint32) {
	purego.SyscallN(d.fn.cmdFillBuffer, uintptr(cb), uintptr(b), uintptr(offset), uintptr(size), uintptr(data))
}

// QueueSubmit is vkQueueSubmit of command buffers with no semaphores and no
// fence.
func (d *Device) QueueSubmit(q Queue, cbs ...CommandBuffer) error {
	if len(cbs) == 0 {
		return nil
	}
	var pinner runtime.Pinner
	defer pinner.Unpin()
	pinner.Pin(&cbs[0])
	info := &SubmitInfo{SType: StructureTypeSubmitInfo, CommandBufferCount: uint32(len(cbs)), PCommandBuffers: &cbs[0]}
	pinner.Pin(info)
	r, _, _ := purego.SyscallN(d.fn.queueSubmit, uintptr(q), 1, uintptr(unsafe.Pointer(info)), 0)
	return result(r)
}

// QueueWaitIdle is vkQueueWaitIdle.
func (d *Device) QueueWaitIdle(q Queue) error {
	r, _, _ := purego.SyscallN(d.fn.queueWaitIdle, uintptr(q))
	return result(r)
}
