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

// Sampling (DESIGN.md "Textures"): Ebitengine reads source texels by pixel
// position (__texelAt), nearest, and a read outside the image is transparent
// — Metal's sampler(coord::pixel, filter::nearest, address::clamp_to_zero).
// The Vulkan sampler for it has unnormalized coordinates, nearest filtering
// and clamp-to-border with a transparent black border; the shader samples
// with an explicit LOD 0 (unnormalized coordinates allow nothing else).

// Sampler is a non-dispatchable handle.
type Sampler uint64

// Sampler and sampling values.
const (
	StructureTypeSamplerCreateInfo StructureType = 31

	FilterNearest                      uint32 = 0
	SamplerMipmapModeNearest           uint32 = 0
	SamplerAddressModeClampToBorder    uint32 = 3
	BorderColorFloatTransparentBlack   uint32 = 0
	DescriptorTypeCombinedImageSampler uint32 = 1
	ImageLayoutShaderReadOnlyOptimal   uint32 = 5
	PipelineStage2FragmentShader       uint64 = 0x80
	Access2ShaderSampledRead           uint64 = 0x100000000
)

// SamplerCreateInfo is VkSamplerCreateInfo.
type SamplerCreateInfo struct {
	SType                   StructureType
	PNext                   unsafe.Pointer
	Flags                   uint32
	MagFilter               uint32
	MinFilter               uint32
	MipmapMode              uint32
	AddressModeU            uint32
	AddressModeV            uint32
	AddressModeW            uint32
	MipLodBias              float32
	AnisotropyEnable        uint32
	MaxAnisotropy           float32
	CompareEnable           uint32
	CompareOp               uint32
	MinLod                  float32
	MaxLod                  float32
	BorderColor             uint32
	UnnormalizedCoordinates uint32
}

// DescriptorImageInfo is VkDescriptorImageInfo.
type DescriptorImageInfo struct {
	Sampler     Sampler
	ImageView   ImageView
	ImageLayout uint32
}

// CreateTexelSampler is vkCreateSampler of Ebitengine's texel sampler:
// unnormalized coordinates, nearest, clamp-to-border transparent black.
func (d *Device) CreateTexelSampler() (Sampler, error) {
	info := &SamplerCreateInfo{
		SType: StructureTypeSamplerCreateInfo, MagFilter: FilterNearest, MinFilter: FilterNearest, MipmapMode: SamplerMipmapModeNearest,
		AddressModeU: SamplerAddressModeClampToBorder, AddressModeV: SamplerAddressModeClampToBorder, AddressModeW: SamplerAddressModeClampToBorder,
		MaxAnisotropy: 1, BorderColor: BorderColorFloatTransparentBlack, UnnormalizedCoordinates: 1,
	}
	var s Sampler
	r, _, _ := purego.SyscallN(d.fn.createSampler, d.handle, uintptr(unsafe.Pointer(info)), 0, uintptr(unsafe.Pointer(&s)))
	runtime.KeepAlive(info)
	return s, result(r)
}

// DestroySampler is vkDestroySampler.
func (d *Device) DestroySampler(s Sampler) {
	purego.SyscallN(d.fn.destroySampler, d.handle, uintptr(s), 0)
}

// WriteCombinedImageSampler is vkUpdateDescriptorSets writing one combined
// image sampler (an image view in a layout, with a sampler) at a binding.
func (d *Device) WriteCombinedImageSampler(set DescriptorSet, binding uint32, s Sampler, view ImageView, layout uint32) {
	var pinner runtime.Pinner
	defer pinner.Unpin()
	img := &DescriptorImageInfo{Sampler: s, ImageView: view, ImageLayout: layout}
	pinner.Pin(img)
	w := &WriteDescriptorSet{SType: StructureTypeWriteDescriptorSet, DstSet: set, DstBinding: binding, DescriptorCount: 1,
		DescriptorType: DescriptorTypeCombinedImageSampler, PImageInfo: uintptr(unsafe.Pointer(img))}
	pinner.Pin(w)
	purego.SyscallN(d.fn.updateDescriptorSets, d.handle, 1, uintptr(unsafe.Pointer(w)), 0, 0)
}

// CmdCopyBufferToImage is vkCmdCopyBufferToImage (the image in layout).
func (d *Device) CmdCopyBufferToImage(cb CommandBuffer, b Buffer, img Image, layout uint32, regions ...BufferImageCopy) {
	purego.SyscallN(d.fn.cmdCopyBufferToImage, uintptr(cb), uintptr(b), uintptr(img), uintptr(layout), uintptr(len(regions)), uintptr(unsafe.Pointer(&regions[0])))
	runtime.KeepAlive(regions)
}
