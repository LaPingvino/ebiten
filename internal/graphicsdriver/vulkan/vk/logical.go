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

// Structure types used by the device functions.
const (
	StructureTypeDeviceQueueCreateInfo          StructureType = 2
	StructureTypeDeviceCreateInfo               StructureType = 3
	StructureTypePhysicalDeviceVulkan13Features StructureType = 53
	StructureTypePhysicalDeviceFeatures2        StructureType = 1000059000
)

// Device and Queue are dispatchable handles.
type (
	Device uintptr
	Queue  uintptr
)

// DeviceQueueCreateInfo is VkDeviceQueueCreateInfo.
type DeviceQueueCreateInfo struct {
	SType            StructureType
	PNext            uintptr
	Flags            uint32
	QueueFamilyIndex uint32
	QueueCount       uint32
	PQueuePriorities *float32
}

// DeviceCreateInfo is VkDeviceCreateInfo.
type DeviceCreateInfo struct {
	SType                   StructureType
	PNext                   unsafe.Pointer
	Flags                   uint32
	QueueCreateInfoCount    uint32
	PQueueCreateInfos       *DeviceQueueCreateInfo
	EnabledLayerCount       uint32
	PPEnabledLayerNames     **byte
	EnabledExtensionCount   uint32
	PPEnabledExtensionNames **byte
	PEnabledFeatures        uintptr
}

// PhysicalDeviceVulkan13Features is VkPhysicalDeviceVulkan13Features (each
// field a VkBool32).
type PhysicalDeviceVulkan13Features struct {
	SType                                              StructureType
	PNext                                              unsafe.Pointer
	RobustImageAccess                                  uint32
	InlineUniformBlock                                 uint32
	DescriptorBindingInlineUniformBlockUpdateAfterBind uint32
	PipelineCreationCacheControl                       uint32
	PrivateData                                        uint32
	ShaderDemoteToHelperInvocation                     uint32
	ShaderTerminateInvocation                          uint32
	SubgroupSizeControl                                uint32
	ComputeFullSubgroups                               uint32
	Synchronization2                                   uint32
	TextureCompressionASTCHDR                          uint32
	ShaderZeroInitializeWorkgroupMemory                uint32
	DynamicRendering                                   uint32
	ShaderIntegerDotProduct                            uint32
	Maintenance4                                       uint32
}

// physicalDeviceFeatures2 is VkPhysicalDeviceFeatures2: the 1.0 features
// (55 VkBool32s, not read yet) with a chain behind it.
type physicalDeviceFeatures2 struct {
	SType    StructureType
	PNext    unsafe.Pointer
	Features [55]uint32
}

// GetPhysicalDeviceVulkan13Features is vkGetPhysicalDeviceFeatures2 asking for
// the Vulkan 1.3 features.
func GetPhysicalDeviceVulkan13Features(instance Instance, device PhysicalDevice) (PhysicalDeviceVulkan13Features, error) {
	fn := procAddr(instance, "vkGetPhysicalDeviceFeatures2")
	if fn == 0 {
		return PhysicalDeviceVulkan13Features{}, fmt.Errorf("vk: vkGetPhysicalDeviceFeatures2 is not available")
	}
	f13 := &PhysicalDeviceVulkan13Features{SType: StructureTypePhysicalDeviceVulkan13Features}
	f2 := &physicalDeviceFeatures2{SType: StructureTypePhysicalDeviceFeatures2, PNext: unsafe.Pointer(f13)}
	var pinner runtime.Pinner
	defer pinner.Unpin()
	pinner.Pin(f13)
	pinner.Pin(f2)
	purego.SyscallN(fn, uintptr(device), uintptr(unsafe.Pointer(f2)))
	return *f13, nil
}

// CreateDevice is vkCreateDevice with one queue of the given family and the
// given Vulkan 1.3 features enabled (nil: none).
func CreateDevice(instance Instance, physical PhysicalDevice, queueFamily uint32, features13 *PhysicalDeviceVulkan13Features, extensions []string) (Device, error) {
	fn := procAddr(instance, "vkCreateDevice")
	if fn == 0 {
		return 0, fmt.Errorf("vk: vkCreateDevice is not available")
	}
	var pinner runtime.Pinner
	defer pinner.Unpin()

	priority := new(float32)
	*priority = 1
	pinner.Pin(priority)
	queue := &DeviceQueueCreateInfo{
		SType:            StructureTypeDeviceQueueCreateInfo,
		QueueFamilyIndex: queueFamily,
		QueueCount:       1,
		PQueuePriorities: priority,
	}
	pinner.Pin(queue)
	info := &DeviceCreateInfo{
		SType:                StructureTypeDeviceCreateInfo,
		QueueCreateInfoCount: 1,
		PQueueCreateInfos:    queue,
	}
	if features13 != nil {
		f := *features13
		f.SType, f.PNext = StructureTypePhysicalDeviceVulkan13Features, nil
		pinner.Pin(&f)
		info.PNext = unsafe.Pointer(&f)
	}
	if len(extensions) > 0 {
		names := make([]*byte, len(extensions))
		for i, e := range extensions {
			names[i] = cstr(e)
			pinner.Pin(names[i])
		}
		pinner.Pin(&names[0])
		info.EnabledExtensionCount = uint32(len(names))
		info.PPEnabledExtensionNames = &names[0]
	}
	pinner.Pin(info)

	var device Device
	r, _, _ := purego.SyscallN(fn, uintptr(physical), uintptr(unsafe.Pointer(info)), 0, uintptr(unsafe.Pointer(&device)))
	if err := result(r); err != nil {
		return 0, err
	}
	return device, nil
}

// deviceProcAddr is vkGetDeviceProcAddr(device, name); 0 when the function
// is not available.
func deviceProcAddr(instance Instance, device Device, name string) uintptr {
	get := procAddr(instance, "vkGetDeviceProcAddr")
	if get == 0 {
		return 0
	}
	n := cstr(name)
	r, _, _ := purego.SyscallN(get, uintptr(device), uintptr(unsafe.Pointer(n)))
	runtime.KeepAlive(n)
	return r
}

// GetDeviceQueue is vkGetDeviceQueue.
func GetDeviceQueue(instance Instance, device Device, family, index uint32) Queue {
	var q Queue
	if fn := deviceProcAddr(instance, device, "vkGetDeviceQueue"); fn != 0 {
		purego.SyscallN(fn, uintptr(device), uintptr(family), uintptr(index), uintptr(unsafe.Pointer(&q)))
	}
	return q
}

// DeviceWaitIdle is vkDeviceWaitIdle.
func DeviceWaitIdle(instance Instance, device Device) error {
	fn := deviceProcAddr(instance, device, "vkDeviceWaitIdle")
	if fn == 0 {
		return fmt.Errorf("vk: vkDeviceWaitIdle is not available")
	}
	r, _, _ := purego.SyscallN(fn, uintptr(device))
	return result(r)
}

// DestroyDevice is vkDestroyDevice.
func DestroyDevice(instance Instance, device Device) {
	if fn := deviceProcAddr(instance, device, "vkDestroyDevice"); fn != 0 {
		purego.SyscallN(fn, uintptr(device), 0)
	}
}
