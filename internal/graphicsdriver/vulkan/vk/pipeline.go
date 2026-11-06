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

// Structure types used by views, pipelines and dynamic rendering.
const (
	StructureTypeImageViewCreateInfo                  StructureType = 15
	StructureTypePipelineShaderStageCreateInfo        StructureType = 18
	StructureTypePipelineVertexInputStateCreateInfo   StructureType = 19
	StructureTypePipelineInputAssemblyStateCreateInfo StructureType = 20
	StructureTypePipelineViewportStateCreateInfo      StructureType = 22
	StructureTypePipelineRasterizationStateCreateInfo StructureType = 23
	StructureTypePipelineMultisampleStateCreateInfo   StructureType = 24
	StructureTypePipelineColorBlendStateCreateInfo    StructureType = 26
	StructureTypePipelineDynamicStateCreateInfo       StructureType = 27
	StructureTypeGraphicsPipelineCreateInfo           StructureType = 28
	StructureTypePipelineLayoutCreateInfo             StructureType = 30
	StructureTypeRenderingInfo                        StructureType = 1000044000
	StructureTypeRenderingAttachmentInfo              StructureType = 1000044001
	StructureTypePipelineRenderingCreateInfo          StructureType = 1000044002
)

// Enum and flag values for views, pipelines and rendering.
const (
	ImageLayoutColorAttachmentOptimal uint32 = 2
	ImageViewType2D                   uint32 = 1
	PrimitiveTopologyTriangleList     uint32 = 3
	DynamicStateViewport              uint32 = 0
	DynamicStateScissor               uint32 = 1
	AttachmentLoadOpClear             uint32 = 1
	AttachmentStoreOpStore            uint32 = 0
	ShaderStageVertexBit              uint32 = 0x1
	ShaderStageFragmentBit            uint32 = 0x10
	SampleCount1Bit                   uint32 = 0x1
	ColorComponentRGBA                uint32 = 0xF // R | G | B | A
	PipelineBindPointGraphics         uint32 = 0

	PipelineStage2ColorAttachmentOutput uint64 = 0x400
	Access2ColorAttachmentWrite         uint64 = 0x100
)

// Non-dispatchable handles.
type (
	ImageView      uint64
	PipelineLayout uint64
	Pipeline       uint64
)

// Offset2D, Extent2D and Rect2D are VkOffset2D, VkExtent2D, VkRect2D.
type (
	Offset2D struct{ X, Y int32 }
	Extent2D struct{ Width, Height uint32 }
	Rect2D   struct {
		Offset Offset2D
		Extent Extent2D
	}
)

// Viewport is VkViewport.
type Viewport struct{ X, Y, Width, Height, MinDepth, MaxDepth float32 }

// ComponentMapping is VkComponentMapping (all identity: zero).
type ComponentMapping struct{ R, G, B, A uint32 }

// ImageViewCreateInfo is VkImageViewCreateInfo.
type ImageViewCreateInfo struct {
	SType            StructureType
	PNext            unsafe.Pointer
	Flags            uint32
	Image            Image
	ViewType         uint32
	Format           uint32
	Components       ComponentMapping
	SubresourceRange ImageSubresourceRange
}

// PipelineLayoutCreateInfo is VkPipelineLayoutCreateInfo.
type PipelineLayoutCreateInfo struct {
	SType                  StructureType
	PNext                  unsafe.Pointer
	Flags                  uint32
	SetLayoutCount         uint32
	PSetLayouts            uintptr
	PushConstantRangeCount uint32
	PPushConstantRanges    uintptr
}

// PipelineShaderStageCreateInfo is VkPipelineShaderStageCreateInfo.
type PipelineShaderStageCreateInfo struct {
	SType               StructureType
	PNext               unsafe.Pointer
	Flags               uint32
	Stage               uint32
	Module              ShaderModule
	PName               *byte
	PSpecializationInfo uintptr
}

// PipelineVertexInputStateCreateInfo is VkPipelineVertexInputStateCreateInfo.
type PipelineVertexInputStateCreateInfo struct {
	SType                           StructureType
	PNext                           unsafe.Pointer
	Flags                           uint32
	VertexBindingDescriptionCount   uint32
	PVertexBindingDescriptions      uintptr
	VertexAttributeDescriptionCount uint32
	PVertexAttributeDescriptions    uintptr
}

// VertexInputBindingDescription is VkVertexInputBindingDescription.
type VertexInputBindingDescription struct {
	Binding, Stride, InputRate uint32
}

// VertexInputAttributeDescription is VkVertexInputAttributeDescription.
type VertexInputAttributeDescription struct {
	Location, Binding, Format, Offset uint32
}

// VertexInputRateVertex: a binding advances per vertex.
const VertexInputRateVertex uint32 = 0

// Vertex attribute formats.
const (
	FormatR32G32Sfloat       uint32 = 103
	FormatR32G32B32A32Sfloat uint32 = 109
)

// VertexLayout is a pipeline's vertex input: one binding (0) of Stride bytes
// per vertex, read as Attributes (their Binding ignored: always 0).
type VertexLayout struct {
	Stride     uint32
	Attributes []VertexInputAttributeDescription
}

// PipelineInputAssemblyStateCreateInfo is VkPipelineInputAssemblyStateCreateInfo.
type PipelineInputAssemblyStateCreateInfo struct {
	SType                  StructureType
	PNext                  unsafe.Pointer
	Flags                  uint32
	Topology               uint32
	PrimitiveRestartEnable uint32
}

// PipelineViewportStateCreateInfo is VkPipelineViewportStateCreateInfo.
type PipelineViewportStateCreateInfo struct {
	SType         StructureType
	PNext         unsafe.Pointer
	Flags         uint32
	ViewportCount uint32
	PViewports    uintptr
	ScissorCount  uint32
	PScissors     uintptr
}

// PipelineRasterizationStateCreateInfo is VkPipelineRasterizationStateCreateInfo.
type PipelineRasterizationStateCreateInfo struct {
	SType                   StructureType
	PNext                   unsafe.Pointer
	Flags                   uint32
	DepthClampEnable        uint32
	RasterizerDiscardEnable uint32
	PolygonMode             uint32
	CullMode                uint32
	FrontFace               uint32
	DepthBiasEnable         uint32
	DepthBiasConstantFactor float32
	DepthBiasClamp          float32
	DepthBiasSlopeFactor    float32
	LineWidth               float32
}

// PipelineMultisampleStateCreateInfo is VkPipelineMultisampleStateCreateInfo.
type PipelineMultisampleStateCreateInfo struct {
	SType                 StructureType
	PNext                 unsafe.Pointer
	Flags                 uint32
	RasterizationSamples  uint32
	SampleShadingEnable   uint32
	MinSampleShading      float32
	PSampleMask           uintptr
	AlphaToCoverageEnable uint32
	AlphaToOneEnable      uint32
}

// PipelineColorBlendAttachmentState is VkPipelineColorBlendAttachmentState.
type PipelineColorBlendAttachmentState struct {
	BlendEnable         uint32
	SrcColorBlendFactor uint32
	DstColorBlendFactor uint32
	ColorBlendOp        uint32
	SrcAlphaBlendFactor uint32
	DstAlphaBlendFactor uint32
	AlphaBlendOp        uint32
	ColorWriteMask      uint32
}

// PipelineColorBlendStateCreateInfo is VkPipelineColorBlendStateCreateInfo.
type PipelineColorBlendStateCreateInfo struct {
	SType           StructureType
	PNext           unsafe.Pointer
	Flags           uint32
	LogicOpEnable   uint32
	LogicOp         uint32
	AttachmentCount uint32
	PAttachments    *PipelineColorBlendAttachmentState
	BlendConstants  [4]float32
}

// PipelineDynamicStateCreateInfo is VkPipelineDynamicStateCreateInfo.
type PipelineDynamicStateCreateInfo struct {
	SType             StructureType
	PNext             unsafe.Pointer
	Flags             uint32
	DynamicStateCount uint32
	PDynamicStates    *uint32
}

// PipelineRenderingCreateInfo is VkPipelineRenderingCreateInfo (dynamic
// rendering: the attachments' formats instead of a render pass).
type PipelineRenderingCreateInfo struct {
	SType                   StructureType
	PNext                   unsafe.Pointer
	ViewMask                uint32
	ColorAttachmentCount    uint32
	PColorAttachmentFormats *uint32
	DepthAttachmentFormat   uint32
	StencilAttachmentFormat uint32
}

// GraphicsPipelineCreateInfo is VkGraphicsPipelineCreateInfo.
type GraphicsPipelineCreateInfo struct {
	SType               StructureType
	PNext               unsafe.Pointer
	Flags               uint32
	StageCount          uint32
	PStages             *PipelineShaderStageCreateInfo
	PVertexInputState   *PipelineVertexInputStateCreateInfo
	PInputAssemblyState *PipelineInputAssemblyStateCreateInfo
	PTessellationState  uintptr
	PViewportState      *PipelineViewportStateCreateInfo
	PRasterizationState *PipelineRasterizationStateCreateInfo
	PMultisampleState   *PipelineMultisampleStateCreateInfo
	PDepthStencilState  uintptr
	PColorBlendState    *PipelineColorBlendStateCreateInfo
	PDynamicState       *PipelineDynamicStateCreateInfo
	Layout              PipelineLayout
	RenderPass          uint64
	Subpass             uint32
	BasePipelineHandle  Pipeline
	BasePipelineIndex   int32
}

// RenderingAttachmentInfo is VkRenderingAttachmentInfo (its clearValue a
// colour).
type RenderingAttachmentInfo struct {
	SType              StructureType
	PNext              unsafe.Pointer
	ImageView          ImageView
	ImageLayout        uint32
	ResolveMode        uint32
	ResolveImageView   ImageView
	ResolveImageLayout uint32
	LoadOp             uint32
	StoreOp            uint32
	ClearValue         [4]float32
}

// RenderingInfo is VkRenderingInfo.
type RenderingInfo struct {
	SType                StructureType
	PNext                unsafe.Pointer
	Flags                uint32
	RenderArea           Rect2D
	LayerCount           uint32
	ViewMask             uint32
	ColorAttachmentCount uint32
	PColorAttachments    *RenderingAttachmentInfo
	PDepthAttachment     uintptr
	PStencilAttachment   uintptr
}

// CreateImageView2D is vkCreateImageView of a 2D image's whole colour range.
func (d *Device) CreateImageView2D(img Image, format uint32) (ImageView, error) {
	info := &ImageViewCreateInfo{SType: StructureTypeImageViewCreateInfo, Image: img, ViewType: ImageViewType2D, Format: format, SubresourceRange: ColorRange}
	var v ImageView
	r, _, _ := purego.SyscallN(d.fn.createImageView, d.handle, uintptr(unsafe.Pointer(info)), 0, uintptr(unsafe.Pointer(&v)))
	runtime.KeepAlive(info)
	return v, result(r)
}

// DestroyImageView is vkDestroyImageView.
func (d *Device) DestroyImageView(v ImageView) {
	purego.SyscallN(d.fn.destroyImageView, d.handle, uintptr(v), 0)
}

// CreatePipelineLayout is vkCreatePipelineLayout with no descriptor sets and
// no push constants.
func (d *Device) CreatePipelineLayout(sets ...DescriptorSetLayout) (PipelineLayout, error) {
	var pinner runtime.Pinner
	defer pinner.Unpin()
	info := &PipelineLayoutCreateInfo{SType: StructureTypePipelineLayoutCreateInfo, SetLayoutCount: uint32(len(sets))}
	if len(sets) > 0 {
		pinner.Pin(&sets[0])
		info.PSetLayouts = uintptr(unsafe.Pointer(&sets[0]))
	}
	pinner.Pin(info)
	var l PipelineLayout
	r, _, _ := purego.SyscallN(d.fn.createPipelineLayout, d.handle, uintptr(unsafe.Pointer(info)), 0, uintptr(unsafe.Pointer(&l)))
	return l, result(r)
}

// DestroyPipelineLayout is vkDestroyPipelineLayout.
func (d *Device) DestroyPipelineLayout(l PipelineLayout) {
	purego.SyscallN(d.fn.destroyPipelineLayout, d.handle, uintptr(l), 0)
}

// CreateGraphicsPipeline is vkCreateGraphicsPipelines of one pipeline for
// dynamic rendering into one colour attachment of a format: a vertex and a
// fragment shader ("main" each), the vertex input of vertices (nil: none —
// the shader makes its own positions), triangle lists, filled and not
// culled, one sample, blending off, viewport and scissor set while recording.
func (d *Device) CreateGraphicsPipeline(layout PipelineLayout, vertex, fragment ShaderModule, vertices *VertexLayout, colorFormat uint32) (Pipeline, error) {
	var pinner runtime.Pinner
	defer pinner.Unpin()
	pin := func(p any) { pinner.Pin(p) }

	name := cstr("main")
	pin(name)
	stages := []PipelineShaderStageCreateInfo{
		{SType: StructureTypePipelineShaderStageCreateInfo, Stage: ShaderStageVertexBit, Module: vertex, PName: name},
		{SType: StructureTypePipelineShaderStageCreateInfo, Stage: ShaderStageFragmentBit, Module: fragment, PName: name},
	}
	pin(&stages[0])
	vertexInput := &PipelineVertexInputStateCreateInfo{SType: StructureTypePipelineVertexInputStateCreateInfo}
	if vertices != nil && len(vertices.Attributes) > 0 {
		binding := &VertexInputBindingDescription{Stride: vertices.Stride, InputRate: VertexInputRateVertex}
		attrs := make([]VertexInputAttributeDescription, len(vertices.Attributes))
		for i, a := range vertices.Attributes {
			a.Binding = 0
			attrs[i] = a
		}
		pin(binding)
		pin(&attrs[0])
		vertexInput.VertexBindingDescriptionCount, vertexInput.PVertexBindingDescriptions = 1, uintptr(unsafe.Pointer(binding))
		vertexInput.VertexAttributeDescriptionCount, vertexInput.PVertexAttributeDescriptions = uint32(len(attrs)), uintptr(unsafe.Pointer(&attrs[0]))
	}
	inputAssembly := &PipelineInputAssemblyStateCreateInfo{SType: StructureTypePipelineInputAssemblyStateCreateInfo, Topology: PrimitiveTopologyTriangleList}
	viewport := &PipelineViewportStateCreateInfo{SType: StructureTypePipelineViewportStateCreateInfo, ViewportCount: 1, ScissorCount: 1}
	raster := &PipelineRasterizationStateCreateInfo{SType: StructureTypePipelineRasterizationStateCreateInfo, LineWidth: 1}
	multisample := &PipelineMultisampleStateCreateInfo{SType: StructureTypePipelineMultisampleStateCreateInfo, RasterizationSamples: SampleCount1Bit}
	attachment := &PipelineColorBlendAttachmentState{ColorWriteMask: ColorComponentRGBA}
	pin(attachment)
	blend := &PipelineColorBlendStateCreateInfo{SType: StructureTypePipelineColorBlendStateCreateInfo, AttachmentCount: 1, PAttachments: attachment}
	dynamics := []uint32{DynamicStateViewport, DynamicStateScissor}
	pin(&dynamics[0])
	dynamic := &PipelineDynamicStateCreateInfo{SType: StructureTypePipelineDynamicStateCreateInfo, DynamicStateCount: 2, PDynamicStates: &dynamics[0]}
	format := new(uint32)
	*format = colorFormat
	pin(format)
	rendering := &PipelineRenderingCreateInfo{SType: StructureTypePipelineRenderingCreateInfo, ColorAttachmentCount: 1, PColorAttachmentFormats: format}
	for _, p := range []any{vertexInput, inputAssembly, viewport, raster, multisample, blend, dynamic, rendering} {
		pin(p)
	}
	info := &GraphicsPipelineCreateInfo{
		SType: StructureTypeGraphicsPipelineCreateInfo, PNext: unsafe.Pointer(rendering),
		StageCount: 2, PStages: &stages[0], PVertexInputState: vertexInput, PInputAssemblyState: inputAssembly,
		PViewportState: viewport, PRasterizationState: raster, PMultisampleState: multisample,
		PColorBlendState: blend, PDynamicState: dynamic, Layout: layout, BasePipelineIndex: -1,
	}
	pin(info)
	var p Pipeline
	r, _, _ := purego.SyscallN(d.fn.createGraphicsPipelines, d.handle, 0, 1, uintptr(unsafe.Pointer(info)), 0, uintptr(unsafe.Pointer(&p)))
	return p, result(r)
}

// DestroyPipeline is vkDestroyPipeline.
func (d *Device) DestroyPipeline(p Pipeline) {
	purego.SyscallN(d.fn.destroyPipeline, d.handle, uintptr(p), 0)
}

// CmdBeginRendering is vkCmdBeginRendering into one colour attachment, its
// area cleared to clear and kept.
func (d *Device) CmdBeginRendering(cb CommandBuffer, view ImageView, area Rect2D, clear [4]float32) {
	var pinner runtime.Pinner
	defer pinner.Unpin()
	att := &RenderingAttachmentInfo{
		SType: StructureTypeRenderingAttachmentInfo, ImageView: view, ImageLayout: ImageLayoutColorAttachmentOptimal,
		LoadOp: AttachmentLoadOpClear, StoreOp: AttachmentStoreOpStore, ClearValue: clear,
	}
	pinner.Pin(att)
	info := &RenderingInfo{SType: StructureTypeRenderingInfo, RenderArea: area, LayerCount: 1, ColorAttachmentCount: 1, PColorAttachments: att}
	pinner.Pin(info)
	purego.SyscallN(d.fn.cmdBeginRendering, uintptr(cb), uintptr(unsafe.Pointer(info)))
}

// CmdEndRendering is vkCmdEndRendering.
func (d *Device) CmdEndRendering(cb CommandBuffer) {
	purego.SyscallN(d.fn.cmdEndRendering, uintptr(cb))
}

// CmdBindGraphicsPipeline is vkCmdBindPipeline at the graphics bind point.
func (d *Device) CmdBindGraphicsPipeline(cb CommandBuffer, p Pipeline) {
	purego.SyscallN(d.fn.cmdBindPipeline, uintptr(cb), uintptr(PipelineBindPointGraphics), uintptr(p))
}

// CmdSetViewport is vkCmdSetViewport of viewport 0.
func (d *Device) CmdSetViewport(cb CommandBuffer, v Viewport) {
	purego.SyscallN(d.fn.cmdSetViewport, uintptr(cb), 0, 1, uintptr(unsafe.Pointer(&v)))
	runtime.KeepAlive(&v)
}

// CmdSetScissor is vkCmdSetScissor of scissor 0.
func (d *Device) CmdSetScissor(cb CommandBuffer, r Rect2D) {
	purego.SyscallN(d.fn.cmdSetScissor, uintptr(cb), 0, 1, uintptr(unsafe.Pointer(&r)))
	runtime.KeepAlive(&r)
}

// CmdBindVertexBuffer is vkCmdBindVertexBuffers of one buffer at binding 0.
func (d *Device) CmdBindVertexBuffer(cb CommandBuffer, b Buffer, offset uint64) {
	purego.SyscallN(d.fn.cmdBindVertexBuffers, uintptr(cb), 0, 1, uintptr(unsafe.Pointer(&b)), uintptr(unsafe.Pointer(&offset)))
	runtime.KeepAlive(&b)
	runtime.KeepAlive(&offset)
}

// IndexTypeUint32: 32-bit indices (what Ebitengine sends).
const IndexTypeUint32 uint32 = 1

// CmdBindIndexBuffer is vkCmdBindIndexBuffer.
func (d *Device) CmdBindIndexBuffer(cb CommandBuffer, b Buffer, offset uint64, indexType uint32) {
	purego.SyscallN(d.fn.cmdBindIndexBuffer, uintptr(cb), uintptr(b), uintptr(offset), uintptr(indexType))
}

// CmdDrawIndexed is vkCmdDrawIndexed.
func (d *Device) CmdDrawIndexed(cb CommandBuffer, indexCount, instanceCount, firstIndex uint32, vertexOffset int32, firstInstance uint32) {
	purego.SyscallN(d.fn.cmdDrawIndexed, uintptr(cb), uintptr(indexCount), uintptr(instanceCount), uintptr(firstIndex), uintptr(vertexOffset), uintptr(firstInstance))
}

// CmdDraw is vkCmdDraw.
func (d *Device) CmdDraw(cb CommandBuffer, vertexCount, instanceCount, firstVertex, firstInstance uint32) {
	purego.SyscallN(d.fn.cmdDraw, uintptr(cb), uintptr(vertexCount), uintptr(instanceCount), uintptr(firstVertex), uintptr(firstInstance))
}
