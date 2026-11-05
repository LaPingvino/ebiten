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

// Structure types used by the memory and buffer functions.
const (
	StructureTypeMemoryAllocateInfo StructureType = 5
	StructureTypeBufferCreateInfo   StructureType = 12
)

// Non-dispatchable handles are 64-bit on every platform.
type (
	DeviceMemory uint64
	Buffer       uint64
)

// MemoryPropertyFlags is VkMemoryPropertyFlags.
type MemoryPropertyFlags uint32

const (
	MemoryPropertyDeviceLocalBit  MemoryPropertyFlags = 0x1
	MemoryPropertyHostVisibleBit  MemoryPropertyFlags = 0x2
	MemoryPropertyHostCoherentBit MemoryPropertyFlags = 0x4
	MemoryPropertyHostCachedBit   MemoryPropertyFlags = 0x8
)

// MemoryType is VkMemoryType.
type MemoryType struct {
	PropertyFlags MemoryPropertyFlags
	HeapIndex     uint32
}

// MemoryHeap is VkMemoryHeap.
type MemoryHeap struct {
	Size  uint64
	Flags uint32
}

// PhysicalDeviceMemoryProperties is VkPhysicalDeviceMemoryProperties.
type PhysicalDeviceMemoryProperties struct {
	MemoryTypeCount uint32
	MemoryTypes     [32]MemoryType // VK_MAX_MEMORY_TYPES
	MemoryHeapCount uint32
	MemoryHeaps     [16]MemoryHeap // VK_MAX_MEMORY_HEAPS
}

// MemoryRequirements is VkMemoryRequirements.
type MemoryRequirements struct {
	Size           uint64
	Alignment      uint64
	MemoryTypeBits uint32
}

// MemoryAllocateInfo is VkMemoryAllocateInfo.
type MemoryAllocateInfo struct {
	SType           StructureType
	PNext           unsafe.Pointer
	AllocationSize  uint64
	MemoryTypeIndex uint32
}

// BufferUsageFlags is VkBufferUsageFlags.
type BufferUsageFlags uint32

const (
	BufferUsageTransferSrcBit   BufferUsageFlags = 0x1
	BufferUsageTransferDstBit   BufferUsageFlags = 0x2
	BufferUsageUniformBufferBit BufferUsageFlags = 0x10
	BufferUsageIndexBufferBit   BufferUsageFlags = 0x40
	BufferUsageVertexBufferBit  BufferUsageFlags = 0x80
)

// BufferCreateInfo is VkBufferCreateInfo (exclusive sharing: no queue
// family list).
type BufferCreateInfo struct {
	SType                 StructureType
	PNext                 unsafe.Pointer
	Flags                 uint32
	Size                  uint64
	Usage                 BufferUsageFlags
	SharingMode           uint32
	QueueFamilyIndexCount uint32
	PQueueFamilyIndices   *uint32
}

// GetPhysicalDeviceMemoryProperties is vkGetPhysicalDeviceMemoryProperties.
func GetPhysicalDeviceMemoryProperties(instance Instance, device PhysicalDevice) PhysicalDeviceMemoryProperties {
	var p PhysicalDeviceMemoryProperties
	if fn := procAddr(instance, "vkGetPhysicalDeviceMemoryProperties"); fn != 0 {
		purego.SyscallN(fn, uintptr(device), uintptr(unsafe.Pointer(&p)))
	}
	return p
}

// FindMemoryType is the first memory type allowed by typeBits (a
// VkMemoryRequirements' memoryTypeBits) that has every property asked for.
func (p *PhysicalDeviceMemoryProperties) FindMemoryType(typeBits uint32, props MemoryPropertyFlags) (uint32, bool) {
	for i := range p.MemoryTypeCount {
		if typeBits&(1<<i) != 0 && p.MemoryTypes[i].PropertyFlags&props == props {
			return i, true
		}
	}
	return 0, false
}

// CreateBuffer is vkCreateBuffer for a buffer used by one queue family.
func CreateBuffer(instance Instance, device Device, size uint64, usage BufferUsageFlags) (Buffer, error) {
	fn := deviceProcAddr(instance, device, "vkCreateBuffer")
	if fn == 0 {
		return 0, fmt.Errorf("vk: vkCreateBuffer is not available")
	}
	info := &BufferCreateInfo{SType: StructureTypeBufferCreateInfo, Size: size, Usage: usage}
	var b Buffer
	r, _, _ := purego.SyscallN(fn, uintptr(device), uintptr(unsafe.Pointer(info)), 0, uintptr(unsafe.Pointer(&b)))
	runtime.KeepAlive(info)
	return b, result(r)
}

// DestroyBuffer is vkDestroyBuffer.
func DestroyBuffer(instance Instance, device Device, b Buffer) {
	if fn := deviceProcAddr(instance, device, "vkDestroyBuffer"); fn != 0 {
		purego.SyscallN(fn, uintptr(device), uintptr(b), 0)
	}
}

// GetBufferMemoryRequirements is vkGetBufferMemoryRequirements.
func GetBufferMemoryRequirements(instance Instance, device Device, b Buffer) MemoryRequirements {
	var req MemoryRequirements
	if fn := deviceProcAddr(instance, device, "vkGetBufferMemoryRequirements"); fn != 0 {
		purego.SyscallN(fn, uintptr(device), uintptr(b), uintptr(unsafe.Pointer(&req)))
	}
	return req
}

// AllocateMemory is vkAllocateMemory.
func AllocateMemory(instance Instance, device Device, size uint64, typeIndex uint32) (DeviceMemory, error) {
	fn := deviceProcAddr(instance, device, "vkAllocateMemory")
	if fn == 0 {
		return 0, fmt.Errorf("vk: vkAllocateMemory is not available")
	}
	info := &MemoryAllocateInfo{SType: StructureTypeMemoryAllocateInfo, AllocationSize: size, MemoryTypeIndex: typeIndex}
	var m DeviceMemory
	r, _, _ := purego.SyscallN(fn, uintptr(device), uintptr(unsafe.Pointer(info)), 0, uintptr(unsafe.Pointer(&m)))
	runtime.KeepAlive(info)
	return m, result(r)
}

// FreeMemory is vkFreeMemory.
func FreeMemory(instance Instance, device Device, m DeviceMemory) {
	if fn := deviceProcAddr(instance, device, "vkFreeMemory"); fn != 0 {
		purego.SyscallN(fn, uintptr(device), uintptr(m), 0)
	}
}

// BindBufferMemory is vkBindBufferMemory.
func BindBufferMemory(instance Instance, device Device, b Buffer, m DeviceMemory, offset uint64) error {
	fn := deviceProcAddr(instance, device, "vkBindBufferMemory")
	if fn == 0 {
		return fmt.Errorf("vk: vkBindBufferMemory is not available")
	}
	r, _, _ := purego.SyscallN(fn, uintptr(device), uintptr(b), uintptr(m), uintptr(offset))
	return result(r)
}

// MapMemory is vkMapMemory: the memory's bytes [offset, offset+size) as a
// slice, valid until UnmapMemory. The memory must be host-visible.
func MapMemory(instance Instance, device Device, m DeviceMemory, offset, size uint64) ([]byte, error) {
	fn := deviceProcAddr(instance, device, "vkMapMemory")
	if fn == 0 {
		return nil, fmt.Errorf("vk: vkMapMemory is not available")
	}
	var p unsafe.Pointer
	r, _, _ := purego.SyscallN(fn, uintptr(device), uintptr(m), uintptr(offset), uintptr(size), 0, uintptr(unsafe.Pointer(&p)))
	if err := result(r); err != nil {
		return nil, err
	}
	return unsafe.Slice((*byte)(p), size), nil
}

// UnmapMemory is vkUnmapMemory.
func UnmapMemory(instance Instance, device Device, m DeviceMemory) {
	if fn := deviceProcAddr(instance, device, "vkUnmapMemory"); fn != 0 {
		purego.SyscallN(fn, uintptr(device), uintptr(m))
	}
}
