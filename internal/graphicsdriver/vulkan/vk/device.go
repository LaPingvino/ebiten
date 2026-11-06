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
	"fmt"
	"runtime"
	"unsafe"

	"github.com/ebitengine/purego"
)

// Device is a logical device and its functions, fetched once through
// vkGetDeviceProcAddr when it is made: calls go straight to the driver,
// without the loader's dispatch or a lookup per call.
type Device struct {
	handle uintptr
	fn     struct {
		destroyDevice, getDeviceQueue, deviceWaitIdle                                  uintptr
		createBuffer, destroyBuffer, getBufferMemoryRequirements, bindBufferMemory     uintptr
		allocateMemory, freeMemory, mapMemory, unmapMemory                             uintptr
		createCommandPool, destroyCommandPool, allocateCommandBuffers                  uintptr
		beginCommandBuffer, endCommandBuffer, cmdFillBuffer                            uintptr
		queueSubmit, queueWaitIdle                                                     uintptr
		createImage, destroyImage, getImageMemoryRequirements, bindImageMemory         uintptr
		cmdPipelineBarrier2, cmdClearColorImage, cmdCopyImageToBuffer                  uintptr
		createShaderModule, destroyShaderModule                                        uintptr
		createImageView, destroyImageView, createPipelineLayout, destroyPipelineLayout uintptr
		createGraphicsPipelines, destroyPipeline, cmdBeginRendering, cmdEndRendering   uintptr
		cmdBindPipeline, cmdSetViewport, cmdSetScissor, cmdDraw, cmdBindVertexBuffers  uintptr
	}
}

func newDevice(instance Instance, handle uintptr) (*Device, error) {
	get := procAddr(instance, "vkGetDeviceProcAddr")
	if get == 0 {
		return nil, fmt.Errorf("vk: vkGetDeviceProcAddr is not available")
	}
	d := &Device{handle: handle}
	for _, f := range []struct {
		name string
		p    *uintptr
	}{
		{"vkDestroyDevice", &d.fn.destroyDevice},
		{"vkGetDeviceQueue", &d.fn.getDeviceQueue},
		{"vkDeviceWaitIdle", &d.fn.deviceWaitIdle},
		{"vkCreateBuffer", &d.fn.createBuffer},
		{"vkDestroyBuffer", &d.fn.destroyBuffer},
		{"vkGetBufferMemoryRequirements", &d.fn.getBufferMemoryRequirements},
		{"vkBindBufferMemory", &d.fn.bindBufferMemory},
		{"vkAllocateMemory", &d.fn.allocateMemory},
		{"vkFreeMemory", &d.fn.freeMemory},
		{"vkMapMemory", &d.fn.mapMemory},
		{"vkUnmapMemory", &d.fn.unmapMemory},
		{"vkCreateCommandPool", &d.fn.createCommandPool},
		{"vkDestroyCommandPool", &d.fn.destroyCommandPool},
		{"vkAllocateCommandBuffers", &d.fn.allocateCommandBuffers},
		{"vkBeginCommandBuffer", &d.fn.beginCommandBuffer},
		{"vkEndCommandBuffer", &d.fn.endCommandBuffer},
		{"vkCmdFillBuffer", &d.fn.cmdFillBuffer},
		{"vkQueueSubmit", &d.fn.queueSubmit},
		{"vkQueueWaitIdle", &d.fn.queueWaitIdle},
		{"vkCreateImage", &d.fn.createImage},
		{"vkDestroyImage", &d.fn.destroyImage},
		{"vkGetImageMemoryRequirements", &d.fn.getImageMemoryRequirements},
		{"vkBindImageMemory", &d.fn.bindImageMemory},
		{"vkCmdPipelineBarrier2", &d.fn.cmdPipelineBarrier2},
		{"vkCmdClearColorImage", &d.fn.cmdClearColorImage},
		{"vkCmdCopyImageToBuffer", &d.fn.cmdCopyImageToBuffer},
		{"vkCreateShaderModule", &d.fn.createShaderModule},
		{"vkDestroyShaderModule", &d.fn.destroyShaderModule},
		{"vkCreateImageView", &d.fn.createImageView},
		{"vkDestroyImageView", &d.fn.destroyImageView},
		{"vkCreatePipelineLayout", &d.fn.createPipelineLayout},
		{"vkDestroyPipelineLayout", &d.fn.destroyPipelineLayout},
		{"vkCreateGraphicsPipelines", &d.fn.createGraphicsPipelines},
		{"vkDestroyPipeline", &d.fn.destroyPipeline},
		{"vkCmdBeginRendering", &d.fn.cmdBeginRendering},
		{"vkCmdEndRendering", &d.fn.cmdEndRendering},
		{"vkCmdBindPipeline", &d.fn.cmdBindPipeline},
		{"vkCmdSetViewport", &d.fn.cmdSetViewport},
		{"vkCmdSetScissor", &d.fn.cmdSetScissor},
		{"vkCmdDraw", &d.fn.cmdDraw},
		{"vkCmdBindVertexBuffers", &d.fn.cmdBindVertexBuffers},
	} {
		n := cstr(f.name)
		*f.p, _, _ = purego.SyscallN(get, handle, uintptr(unsafe.Pointer(n)))
		runtime.KeepAlive(n)
		if *f.p == 0 {
			if d.fn.destroyDevice != 0 {
				purego.SyscallN(d.fn.destroyDevice, handle, 0)
			}
			return nil, fmt.Errorf("vk: the device has no %s", f.name)
		}
	}
	return d, nil
}

// Handle is the VkDevice.
func (d *Device) Handle() uintptr { return d.handle }

// Destroy is vkDestroyDevice.
func (d *Device) Destroy() {
	purego.SyscallN(d.fn.destroyDevice, d.handle, 0)
	d.handle = 0
}

// Queue is vkGetDeviceQueue.
func (d *Device) Queue(family, index uint32) Queue {
	var q Queue
	purego.SyscallN(d.fn.getDeviceQueue, d.handle, uintptr(family), uintptr(index), uintptr(unsafe.Pointer(&q)))
	return q
}

// WaitIdle is vkDeviceWaitIdle.
func (d *Device) WaitIdle() error {
	r, _, _ := purego.SyscallN(d.fn.deviceWaitIdle, d.handle)
	return result(r)
}

// CreateBuffer is vkCreateBuffer for a buffer used by one queue family.
func (d *Device) CreateBuffer(size uint64, usage BufferUsageFlags) (Buffer, error) {
	info := &BufferCreateInfo{SType: StructureTypeBufferCreateInfo, Size: size, Usage: usage}
	var b Buffer
	r, _, _ := purego.SyscallN(d.fn.createBuffer, d.handle, uintptr(unsafe.Pointer(info)), 0, uintptr(unsafe.Pointer(&b)))
	runtime.KeepAlive(info)
	return b, result(r)
}

// DestroyBuffer is vkDestroyBuffer.
func (d *Device) DestroyBuffer(b Buffer) {
	purego.SyscallN(d.fn.destroyBuffer, d.handle, uintptr(b), 0)
}

// BufferMemoryRequirements is vkGetBufferMemoryRequirements.
func (d *Device) BufferMemoryRequirements(b Buffer) MemoryRequirements {
	var req MemoryRequirements
	purego.SyscallN(d.fn.getBufferMemoryRequirements, d.handle, uintptr(b), uintptr(unsafe.Pointer(&req)))
	return req
}

// BindBufferMemory is vkBindBufferMemory.
func (d *Device) BindBufferMemory(b Buffer, m DeviceMemory, offset uint64) error {
	r, _, _ := purego.SyscallN(d.fn.bindBufferMemory, d.handle, uintptr(b), uintptr(m), uintptr(offset))
	return result(r)
}

// AllocateMemory is vkAllocateMemory.
func (d *Device) AllocateMemory(size uint64, typeIndex uint32) (DeviceMemory, error) {
	info := &MemoryAllocateInfo{SType: StructureTypeMemoryAllocateInfo, AllocationSize: size, MemoryTypeIndex: typeIndex}
	var m DeviceMemory
	r, _, _ := purego.SyscallN(d.fn.allocateMemory, d.handle, uintptr(unsafe.Pointer(info)), 0, uintptr(unsafe.Pointer(&m)))
	runtime.KeepAlive(info)
	return m, result(r)
}

// FreeMemory is vkFreeMemory.
func (d *Device) FreeMemory(m DeviceMemory) {
	purego.SyscallN(d.fn.freeMemory, d.handle, uintptr(m), 0)
}

// MapMemory is vkMapMemory: the memory's bytes [offset, offset+size) as a
// slice, valid until UnmapMemory. The memory must be host-visible.
func (d *Device) MapMemory(m DeviceMemory, offset, size uint64) ([]byte, error) {
	var p unsafe.Pointer
	r, _, _ := purego.SyscallN(d.fn.mapMemory, d.handle, uintptr(m), uintptr(offset), uintptr(size), 0, uintptr(unsafe.Pointer(&p)))
	if err := result(r); err != nil {
		return nil, err
	}
	return unsafe.Slice((*byte)(p), size), nil
}

// UnmapMemory is vkUnmapMemory.
func (d *Device) UnmapMemory(m DeviceMemory) {
	purego.SyscallN(d.fn.unmapMemory, d.handle, uintptr(m))
}
