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

//go:build (freebsd || linux || netbsd || openbsd) && !android

package vk

import (
	"runtime"
	"unsafe"

	"github.com/ebitengine/purego"
)

// Fence is VkFence.
type Fence uint64

const (
	StructureTypeFenceCreateInfo StructureType = 8
	FenceCreateSignaledBit       uint32        = 0x1
	// NotReady and Timeout are VK_NOT_READY / VK_TIMEOUT: not errors, a
	// fence not signaled yet.
	NotReady Result = 1
	Timeout  Result = 2
)

// FenceCreateInfo is VkFenceCreateInfo.
type FenceCreateInfo struct {
	SType StructureType
	PNext unsafe.Pointer
	Flags uint32
}

// CreateFence is vkCreateFence (signaled: made signaled).
func (d *Device) CreateFence(signaled bool) (Fence, error) {
	info := &FenceCreateInfo{SType: StructureTypeFenceCreateInfo}
	if signaled {
		info.Flags = FenceCreateSignaledBit
	}
	var pinner runtime.Pinner
	defer pinner.Unpin()
	pinner.Pin(info)
	var f Fence
	r, _, _ := purego.SyscallN(d.fn.createFence, d.handle, uintptr(unsafe.Pointer(info)), 0, uintptr(unsafe.Pointer(&f)))
	return f, result(r)
}

// DestroyFence is vkDestroyFence.
func (d *Device) DestroyFence(f Fence) {
	purego.SyscallN(d.fn.destroyFence, d.handle, uintptr(f), 0)
}

// WaitForFence is vkWaitForFences of one fence, without a time limit.
func (d *Device) WaitForFence(f Fence) error {
	r, _, _ := purego.SyscallN(d.fn.waitForFences, d.handle, 1, uintptr(unsafe.Pointer(&f)), 1, ^uintptr(0))
	runtime.KeepAlive(&f)
	return result(r)
}

// ResetFence is vkResetFences of one fence.
func (d *Device) ResetFence(f Fence) error {
	r, _, _ := purego.SyscallN(d.fn.resetFences, d.handle, 1, uintptr(unsafe.Pointer(&f)))
	runtime.KeepAlive(&f)
	return result(r)
}

// FenceSignaled is vkGetFenceStatus: whether the fence is signaled
// (VK_NOT_READY: not yet, no error).
func (d *Device) FenceSignaled(f Fence) (bool, error) {
	r, _, _ := purego.SyscallN(d.fn.getFenceStatus, d.handle, uintptr(f))
	switch Result(int32(r)) {
	case Success:
		return true, nil
	case NotReady:
		return false, nil
	}
	return false, result(r)
}

// QueueSubmitFence is vkQueueSubmit of command buffers that signals fence
// when they have completed.
func (d *Device) QueueSubmitFence(q Queue, f Fence, cbs ...CommandBuffer) error {
	var pinner runtime.Pinner
	defer pinner.Unpin()
	info := &SubmitInfo{SType: StructureTypeSubmitInfo}
	if len(cbs) > 0 {
		pinner.Pin(&cbs[0])
		info.CommandBufferCount, info.PCommandBuffers = uint32(len(cbs)), &cbs[0]
	}
	pinner.Pin(info)
	r, _, _ := purego.SyscallN(d.fn.queueSubmit, uintptr(q), 1, uintptr(unsafe.Pointer(info)), uintptr(f))
	return result(r)
}
