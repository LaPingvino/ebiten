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

// Structure types used by the image functions.
const (
	StructureTypeImageCreateInfo     StructureType = 14
	StructureTypeImageMemoryBarrier2 StructureType = 1000314002
	StructureTypeDependencyInfo      StructureType = 1000314003
)

// Image is a non-dispatchable handle.
type Image uint64

// Format, layout, type, usage and aspect values (VkFormat, VkImageLayout …).
const (
	FormatR8G8B8A8Unorm uint32 = 37

	ImageLayoutUndefined          uint32 = 0
	ImageLayoutGeneral            uint32 = 1
	ImageLayoutTransferSrcOptimal uint32 = 6
	ImageLayoutTransferDstOptimal uint32 = 7

	ImageType2D uint32 = 1

	ImageUsageTransferSrcBit     uint32 = 0x1
	ImageUsageTransferDstBit     uint32 = 0x2
	ImageUsageSampledBit         uint32 = 0x4
	ImageUsageColorAttachmentBit uint32 = 0x10

	ImageAspectColorBit uint32 = 0x1

	QueueFamilyIgnored uint32 = ^uint32(0)
)

// Synchronization2's stage and access bits (VkPipelineStageFlags2,
// VkAccessFlags2): 64-bit.
const (
	PipelineStage2None        uint64 = 0
	PipelineStage2AllTransfer uint64 = 0x1000
	PipelineStage2Host        uint64 = 0x4000
	PipelineStage2AllCommands uint64 = 0x10000

	Access2None          uint64 = 0
	Access2TransferRead  uint64 = 0x800
	Access2TransferWrite uint64 = 0x1000
	Access2HostRead      uint64 = 0x2000
	Access2MemoryRead    uint64 = 0x8000
	Access2MemoryWrite   uint64 = 0x10000

	Access2ShaderRead          uint64 = 0x20
	Access2ColorAttachmentRead uint64 = 0x80
)

// Extent3D is VkExtent3D; Offset3D VkOffset3D.
type (
	Extent3D struct{ Width, Height, Depth uint32 }
	Offset3D struct{ X, Y, Z int32 }
)

// ImageCreateInfo is VkImageCreateInfo.
type ImageCreateInfo struct {
	SType                 StructureType
	PNext                 unsafe.Pointer
	Flags                 uint32
	ImageType             uint32
	Format                uint32
	Extent                Extent3D
	MipLevels             uint32
	ArrayLayers           uint32
	Samples               uint32
	Tiling                uint32
	Usage                 uint32
	SharingMode           uint32
	QueueFamilyIndexCount uint32
	PQueueFamilyIndices   *uint32
	InitialLayout         uint32
}

// ImageSubresourceRange is VkImageSubresourceRange.
type ImageSubresourceRange struct {
	AspectMask     uint32
	BaseMipLevel   uint32
	LevelCount     uint32
	BaseArrayLayer uint32
	LayerCount     uint32
}

// ImageSubresourceLayers is VkImageSubresourceLayers.
type ImageSubresourceLayers struct {
	AspectMask     uint32
	MipLevel       uint32
	BaseArrayLayer uint32
	LayerCount     uint32
}

// ImageMemoryBarrier2 is VkImageMemoryBarrier2.
type ImageMemoryBarrier2 struct {
	SType               StructureType
	PNext               unsafe.Pointer
	SrcStageMask        uint64
	SrcAccessMask       uint64
	DstStageMask        uint64
	DstAccessMask       uint64
	OldLayout           uint32
	NewLayout           uint32
	SrcQueueFamilyIndex uint32
	DstQueueFamilyIndex uint32
	Image               Image
	SubresourceRange    ImageSubresourceRange
}

// DependencyInfo is VkDependencyInfo.
type DependencyInfo struct {
	SType                    StructureType
	PNext                    unsafe.Pointer
	DependencyFlags          uint32
	MemoryBarrierCount       uint32
	PMemoryBarriers          uintptr
	BufferMemoryBarrierCount uint32
	PBufferMemoryBarriers    uintptr
	ImageMemoryBarrierCount  uint32
	PImageMemoryBarriers     *ImageMemoryBarrier2
}

// BufferImageCopy is VkBufferImageCopy.
type BufferImageCopy struct {
	BufferOffset      uint64
	BufferRowLength   uint32
	BufferImageHeight uint32
	ImageSubresource  ImageSubresourceLayers
	ImageOffset       Offset3D
	ImageExtent       Extent3D
}

// ColorRange is the whole colour aspect of a one-level, one-layer image.
var ColorRange = ImageSubresourceRange{AspectMask: ImageAspectColorBit, LevelCount: 1, LayerCount: 1}

// CreateImage2D is vkCreateImage for a 2D, one-level, one-layer,
// single-sampled, optimally tiled image used by one queue family.
func (d *Device) CreateImage2D(width, height, format, usage uint32) (Image, error) {
	info := &ImageCreateInfo{
		SType: StructureTypeImageCreateInfo, ImageType: ImageType2D, Format: format,
		Extent: Extent3D{width, height, 1}, MipLevels: 1, ArrayLayers: 1, Samples: 1, Usage: usage,
		InitialLayout: ImageLayoutUndefined,
	}
	var img Image
	r, _, _ := purego.SyscallN(d.fn.createImage, d.handle, uintptr(unsafe.Pointer(info)), 0, uintptr(unsafe.Pointer(&img)))
	runtime.KeepAlive(info)
	return img, result(r)
}

// DestroyImage is vkDestroyImage.
func (d *Device) DestroyImage(img Image) {
	purego.SyscallN(d.fn.destroyImage, d.handle, uintptr(img), 0)
}

// ImageMemoryRequirements is vkGetImageMemoryRequirements.
func (d *Device) ImageMemoryRequirements(img Image) MemoryRequirements {
	var req MemoryRequirements
	purego.SyscallN(d.fn.getImageMemoryRequirements, d.handle, uintptr(img), uintptr(unsafe.Pointer(&req)))
	return req
}

// BindImageMemory is vkBindImageMemory.
func (d *Device) BindImageMemory(img Image, m DeviceMemory, offset uint64) error {
	r, _, _ := purego.SyscallN(d.fn.bindImageMemory, d.handle, uintptr(img), uintptr(m), uintptr(offset))
	return result(r)
}

// CmdImageBarrier is vkCmdPipelineBarrier2 with image barriers only (the
// device must have synchronization2 enabled).
func (d *Device) CmdImageBarrier(cb CommandBuffer, barriers ...ImageMemoryBarrier2) {
	if len(barriers) == 0 {
		return
	}
	var pinner runtime.Pinner
	defer pinner.Unpin()
	for i := range barriers {
		barriers[i].SType = StructureTypeImageMemoryBarrier2
	}
	pinner.Pin(&barriers[0])
	dep := &DependencyInfo{SType: StructureTypeDependencyInfo, ImageMemoryBarrierCount: uint32(len(barriers)), PImageMemoryBarriers: &barriers[0]}
	pinner.Pin(dep)
	purego.SyscallN(d.fn.cmdPipelineBarrier2, uintptr(cb), uintptr(unsafe.Pointer(dep)))
}

// CmdClearColorImage is vkCmdClearColorImage of the whole colour range to an
// RGBA colour (floats, for a UNORM / float format).
func (d *Device) CmdClearColorImage(cb CommandBuffer, img Image, layout uint32, rgba [4]float32) {
	rng := ColorRange
	purego.SyscallN(d.fn.cmdClearColorImage, uintptr(cb), uintptr(img), uintptr(layout),
		uintptr(unsafe.Pointer(&rgba)), 1, uintptr(unsafe.Pointer(&rng)))
	runtime.KeepAlive(&rgba)
	runtime.KeepAlive(&rng)
}

// CmdCopyImageToBuffer is vkCmdCopyImageToBuffer of regions.
func (d *Device) CmdCopyImageToBuffer(cb CommandBuffer, img Image, layout uint32, b Buffer, regions ...BufferImageCopy) {
	if len(regions) == 0 {
		return
	}
	var pinner runtime.Pinner
	defer pinner.Unpin()
	pinner.Pin(&regions[0])
	purego.SyscallN(d.fn.cmdCopyImageToBuffer, uintptr(cb), uintptr(img), uintptr(layout), uintptr(b),
		uintptr(len(regions)), uintptr(unsafe.Pointer(&regions[0])))
}
