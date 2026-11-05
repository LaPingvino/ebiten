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
	"bytes"
	"encoding/binary"
	"unsafe"

	"github.com/ebitengine/purego"
)

// PhysicalDeviceType is VkPhysicalDeviceType.
type PhysicalDeviceType uint32

const (
	PhysicalDeviceTypeOther         PhysicalDeviceType = 0
	PhysicalDeviceTypeIntegratedGPU PhysicalDeviceType = 1
	PhysicalDeviceTypeDiscreteGPU   PhysicalDeviceType = 2
	PhysicalDeviceTypeVirtualGPU    PhysicalDeviceType = 3
	PhysicalDeviceTypeCPU           PhysicalDeviceType = 4
)

func (t PhysicalDeviceType) String() string {
	switch t {
	case PhysicalDeviceTypeIntegratedGPU:
		return "integrated GPU"
	case PhysicalDeviceTypeDiscreteGPU:
		return "discrete GPU"
	case PhysicalDeviceTypeVirtualGPU:
		return "virtual GPU"
	case PhysicalDeviceTypeCPU:
		return "CPU"
	}
	return "other"
}

// PhysicalDeviceProperties is the part of VkPhysicalDeviceProperties the
// driver uses.
type PhysicalDeviceProperties struct {
	APIVersion          uint32
	DriverVersion       uint32
	VendorID            uint32
	DeviceID            uint32
	DeviceType          PhysicalDeviceType
	DeviceName          string
	MaxImageDimension2D uint32
}

// The C struct's layout on 64-bit targets: four uint32s, the device type,
// char deviceName[256], uint8_t pipelineCacheUUID[16], then
// VkPhysicalDeviceLimits (8-byte aligned; maxImageDimension2D is its second
// uint32) and VkPhysicalDeviceSparseProperties — 824 bytes in all.
const (
	propsSize         = 824
	propsNameOffset   = 20
	propsNameLen      = 256
	propsLimitsOffset = 296
)

// GetPhysicalDeviceProperties is vkGetPhysicalDeviceProperties.
func GetPhysicalDeviceProperties(instance Instance, device PhysicalDevice) PhysicalDeviceProperties {
	var buf [propsSize]byte
	if fn := procAddr(instance, "vkGetPhysicalDeviceProperties"); fn != 0 {
		purego.SyscallN(fn, uintptr(device), uintptr(unsafe.Pointer(&buf[0])))
	}
	u32 := func(off int) uint32 { return binary.NativeEndian.Uint32(buf[off:]) }
	name := buf[propsNameOffset : propsNameOffset+propsNameLen]
	if i := bytes.IndexByte(name, 0); i >= 0 {
		name = name[:i]
	}
	return PhysicalDeviceProperties{
		APIVersion:          u32(0),
		DriverVersion:       u32(4),
		VendorID:            u32(8),
		DeviceID:            u32(12),
		DeviceType:          PhysicalDeviceType(u32(16)),
		DeviceName:          string(name),
		MaxImageDimension2D: u32(propsLimitsOffset + 4),
	}
}

// QueueFlags is VkQueueFlags.
type QueueFlags uint32

const (
	QueueGraphicsBit QueueFlags = 0x1
	QueueComputeBit  QueueFlags = 0x2
	QueueTransferBit QueueFlags = 0x4
)

// QueueFamilyProperties is VkQueueFamilyProperties.
type QueueFamilyProperties struct {
	QueueFlags                  QueueFlags
	QueueCount                  uint32
	TimestampValidBits          uint32
	MinImageTransferGranularity [3]uint32
}

// GetPhysicalDeviceQueueFamilyProperties is
// vkGetPhysicalDeviceQueueFamilyProperties.
func GetPhysicalDeviceQueueFamilyProperties(instance Instance, device PhysicalDevice) []QueueFamilyProperties {
	fn := procAddr(instance, "vkGetPhysicalDeviceQueueFamilyProperties")
	if fn == 0 {
		return nil
	}
	var n uint32
	purego.SyscallN(fn, uintptr(device), uintptr(unsafe.Pointer(&n)), 0)
	if n == 0 {
		return nil
	}
	families := make([]QueueFamilyProperties, n)
	purego.SyscallN(fn, uintptr(device), uintptr(unsafe.Pointer(&n)), uintptr(unsafe.Pointer(&families[0])))
	return families[:n]
}
