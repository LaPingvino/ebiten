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

// Package vk is a binding of the part of the Vulkan API the vulkan graphics
// driver uses, without cgo: the Vulkan loader is opened with purego, and every
// other function is fetched through vkGetInstanceProcAddr.
//
// Structs are laid out as their C counterparts on 64-bit targets. Go memory
// that a struct points to is pinned (runtime.Pinner) for the length of the
// call that reads it.
package vk

import "fmt"

// Result is VkResult.
type Result int32

const (
	Success                   Result = 0
	Incomplete                Result = 5
	ErrorOutOfHostMemory      Result = -1
	ErrorOutOfDeviceMemory    Result = -2
	ErrorInitializationFailed Result = -3
	ErrorDeviceLost           Result = -4
	ErrorLayerNotPresent      Result = -6
	ErrorExtensionNotPresent  Result = -7
	ErrorIncompatibleDriver   Result = -9
)

func (r Result) Error() string {
	switch r {
	case Incomplete:
		return "vk: VK_INCOMPLETE"
	case ErrorOutOfHostMemory:
		return "vk: VK_ERROR_OUT_OF_HOST_MEMORY"
	case ErrorOutOfDeviceMemory:
		return "vk: VK_ERROR_OUT_OF_DEVICE_MEMORY"
	case ErrorInitializationFailed:
		return "vk: VK_ERROR_INITIALIZATION_FAILED"
	case ErrorDeviceLost:
		return "vk: VK_ERROR_DEVICE_LOST"
	case ErrorLayerNotPresent:
		return "vk: VK_ERROR_LAYER_NOT_PRESENT"
	case ErrorExtensionNotPresent:
		return "vk: VK_ERROR_EXTENSION_NOT_PRESENT"
	case ErrorIncompatibleDriver:
		return "vk: VK_ERROR_INCOMPATIBLE_DRIVER"
	}
	return fmt.Sprintf("vk: VkResult %d", int32(r))
}

// result turns a returned VkResult into an error (nil for VK_SUCCESS).
func result(r uintptr) error {
	if res := Result(int32(r)); res != Success {
		return res
	}
	return nil
}

// StructureType is VkStructureType.
type StructureType uint32

const (
	StructureTypeApplicationInfo    StructureType = 0
	StructureTypeInstanceCreateInfo StructureType = 1
)

// MakeAPIVersion is VK_MAKE_API_VERSION.
func MakeAPIVersion(variant, major, minor, patch uint32) uint32 {
	return variant<<29 | major<<22 | minor<<12 | patch
}

// APIVersion13 is VK_API_VERSION_1_3, the version the driver asks for.
var APIVersion13 = MakeAPIVersion(0, 1, 3, 0)

// VersionMajor, VersionMinor and VersionPatch take an API version apart.
func VersionMajor(v uint32) uint32 { return v >> 22 & 0x7f }
func VersionMinor(v uint32) uint32 { return v >> 12 & 0x3ff }
func VersionPatch(v uint32) uint32 { return v & 0xfff }

// Instance and PhysicalDevice are dispatchable handles.
type (
	Instance       uintptr
	PhysicalDevice uintptr
)

// ApplicationInfo is VkApplicationInfo.
type ApplicationInfo struct {
	SType              StructureType
	PNext              uintptr
	PApplicationName   *byte
	ApplicationVersion uint32
	PEngineName        *byte
	EngineVersion      uint32
	APIVersion         uint32
}

// InstanceCreateInfo is VkInstanceCreateInfo.
type InstanceCreateInfo struct {
	SType                   StructureType
	PNext                   uintptr
	Flags                   uint32
	PApplicationInfo        *ApplicationInfo
	EnabledLayerCount       uint32
	PPEnabledLayerNames     **byte
	EnabledExtensionCount   uint32
	PPEnabledExtensionNames **byte
}
