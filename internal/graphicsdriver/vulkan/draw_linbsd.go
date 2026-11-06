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

package vulkan

import (
	"errors"
	"fmt"
	"image"
	"unsafe"

	"github.com/hajimehoshi/ebiten/v2/internal/graphics"
	"github.com/hajimehoshi/ebiten/v2/internal/graphicsdriver"
	"github.com/hajimehoshi/ebiten/v2/internal/graphicsdriver/vulkan/vk"
	"github.com/hajimehoshi/ebiten/v2/internal/shaderir"
	"github.com/hajimehoshi/ebiten/v2/internal/shaderir/spirv"
)

// ebitengineVertex is Ebitengine's vertex: the position, the source texel,
// the colour, the custom attributes (graphics.VertexFloatCount floats).
var ebitengineVertex = &vk.VertexLayout{Stride: graphics.VertexFloatCount * 4, Attributes: []vk.VertexInputAttributeDescription{
	{Location: 0, Format: vk.FormatR32G32Sfloat, Offset: 0},
	{Location: 1, Format: vk.FormatR32G32Sfloat, Offset: 2 * 4},
	{Location: 2, Format: vk.FormatR32G32B32A32Sfloat, Offset: 4 * 4},
	{Location: 3, Format: vk.FormatR32G32B32A32Sfloat, Offset: 8 * 4},
}}

const descriptorSetsPerPool = 256

// Shader is a Kage program compiled to SPIR-V, with its pipelines by blend.
type Shader struct {
	g         *Graphics
	id        graphicsdriver.ShaderID
	program   *shaderir.Program
	vs, fs    vk.ShaderModule
	pipelines map[graphicsdriver.Blend]vk.Pipeline
}

func (s *Shader) ID() graphicsdriver.ShaderID { return s.id }

func (s *Shader) Dispose() {
	if s.vs == 0 {
		return
	}
	g, vs, fs, ps := s.g, s.vs, s.fs, s.pipelines
	s.vs, s.fs, s.pipelines = 0, 0, nil
	delete(g.shaders, s.id)
	free := func() {
		for _, p := range ps {
			g.device.DestroyPipeline(p)
		}
		g.device.DestroyShaderModule(vs)
		g.device.DestroyShaderModule(fs)
	}
	if g.recording {
		g.afterBatch = append(g.afterBatch, free)
		return
	}
	free()
}

func (g *Graphics) NewShader(program *shaderir.Program) (graphicsdriver.Shader, error) {
	if err := g.initDrawing(); err != nil {
		return nil, err
	}
	vsWords, err := spirv.CompileVertex(program)
	if err != nil {
		return nil, fmt.Errorf("vulkan: %w", err)
	}
	fsWords, err := spirv.CompileFragment(program)
	if err != nil {
		return nil, fmt.Errorf("vulkan: %w", err)
	}
	vs, err := g.device.CreateShaderModule(vsWords)
	if err != nil {
		return nil, fmt.Errorf("vulkan: %w", err)
	}
	fs, err := g.device.CreateShaderModule(fsWords)
	if err != nil {
		g.device.DestroyShaderModule(vs)
		return nil, fmt.Errorf("vulkan: %w", err)
	}
	g.nextShaderID++
	s := &Shader{g: g, id: g.nextShaderID, program: program, vs: vs, fs: fs, pipelines: map[graphicsdriver.Blend]vk.Pipeline{}}
	g.shaders[s.id] = s
	return s, nil
}

// blendFactors are Ebitengine's blend factors as VkBlendFactor; the
// operations' values are the same in both.
var blendFactors = [...]uint32{
	graphicsdriver.BlendFactorZero:                     0,
	graphicsdriver.BlendFactorOne:                      1,
	graphicsdriver.BlendFactorSourceColor:              2,
	graphicsdriver.BlendFactorOneMinusSourceColor:      3,
	graphicsdriver.BlendFactorDestinationColor:         4,
	graphicsdriver.BlendFactorOneMinusDestinationColor: 5,
	graphicsdriver.BlendFactorSourceAlpha:              6,
	graphicsdriver.BlendFactorOneMinusSourceAlpha:      7,
	graphicsdriver.BlendFactorDestinationAlpha:         8,
	graphicsdriver.BlendFactorOneMinusDestinationAlpha: 9,
	graphicsdriver.BlendFactorSourceAlphaSaturated:     14,
}

func (s *Shader) pipeline(blend graphicsdriver.Blend) (vk.Pipeline, error) {
	if p, ok := s.pipelines[blend]; ok {
		return p, nil
	}
	state := &vk.PipelineColorBlendAttachmentState{
		BlendEnable:         1,
		SrcColorBlendFactor: blendFactors[blend.BlendFactorSourceRGB],
		DstColorBlendFactor: blendFactors[blend.BlendFactorDestinationRGB],
		ColorBlendOp:        uint32(blend.BlendOperationRGB),
		SrcAlphaBlendFactor: blendFactors[blend.BlendFactorSourceAlpha],
		DstAlphaBlendFactor: blendFactors[blend.BlendFactorDestinationAlpha],
		AlphaBlendOp:        uint32(blend.BlendOperationAlpha),
		ColorWriteMask:      vk.ColorComponentRGBA,
	}
	p, err := s.g.device.CreateGraphicsPipelineBlend(s.g.pipelineLayout, s.vs, s.fs, ebitengineVertex, vk.FormatR8G8B8A8Unorm, state)
	if err != nil {
		return 0, fmt.Errorf("vulkan: %w", err)
	}
	s.pipelines[blend] = p
	return p, nil
}

// initDrawing makes what every draw shares: the descriptor set layout (the
// uniform block at binding 0, the four sources at 1–4), the pipeline
// layout, the texel sampler and a 1×1 transparent image standing in for
// absent sources.
func (g *Graphics) initDrawing() error {
	if g.pipelineLayout != 0 {
		return nil
	}
	bindings := []vk.DescriptorSetLayoutBinding{{Binding: 0, DescriptorType: vk.DescriptorTypeUniformBufferDynamic, DescriptorCount: 1,
		StageFlags: vk.ShaderStageVertexBit | vk.ShaderStageFragmentBit}}
	for i := 0; i < graphics.ShaderSrcImageCount; i++ {
		bindings = append(bindings, vk.DescriptorSetLayoutBinding{Binding: uint32(1 + i), DescriptorType: vk.DescriptorTypeCombinedImageSampler, DescriptorCount: 1,
			StageFlags: vk.ShaderStageFragmentBit})
	}
	var err error
	if g.setLayout, err = g.device.CreateDescriptorSetLayout(bindings...); err != nil {
		return fmt.Errorf("vulkan: %w", err)
	}
	if g.pipelineLayout, err = g.device.CreatePipelineLayout(g.setLayout); err != nil {
		return fmt.Errorf("vulkan: %w", err)
	}
	if g.sampler, err = g.device.CreateTexelSampler(); err != nil {
		return fmt.Errorf("vulkan: %w", err)
	}
	empty, err := g.NewImage(1, 1)
	if err != nil {
		return err
	}
	g.emptyImage = empty.(*Image)
	delete(g.images, g.emptyImage.id) // the driver's own, not one of Ebitengine's
	return g.emptyImage.WritePixels([]graphicsdriver.PixelsArgs{{Pixels: make([]byte, 4), Region: g.emptyImage.bounds()}})
}

func (g *Graphics) destroyDrawing() {
	if g.pipelineLayout == 0 {
		return
	}
	for _, s := range g.shaders {
		s.Dispose()
	}
	g.emptyImage.Dispose()
	if g.descPool != 0 {
		g.device.DestroyDescriptorPool(g.descPool)
	}
	g.device.DestroySampler(g.sampler)
	g.device.DestroyPipelineLayout(g.pipelineLayout)
	g.device.DestroyDescriptorSetLayout(g.setLayout)
}

// descriptorSet is a new set from the batch's pool (pools are destroyed
// after their batch).
func (g *Graphics) descriptorSet() (vk.DescriptorSet, error) {
	if g.descPool == 0 || g.descUsed == descriptorSetsPerPool {
		if g.descPool != 0 {
			old := g.descPool
			g.afterBatch = append(g.afterBatch, func() { g.device.DestroyDescriptorPool(old) })
		}
		pool, err := g.device.CreateDescriptorPool(descriptorSetsPerPool,
			vk.DescriptorPoolSize{Type: vk.DescriptorTypeUniformBufferDynamic, DescriptorCount: descriptorSetsPerPool},
			vk.DescriptorPoolSize{Type: vk.DescriptorTypeCombinedImageSampler, DescriptorCount: descriptorSetsPerPool * graphics.ShaderSrcImageCount})
		if err != nil {
			return 0, fmt.Errorf("vulkan: %w", err)
		}
		g.descPool, g.descUsed = pool, 0
	}
	set, err := g.device.AllocateDescriptorSet(g.descPool, g.setLayout)
	if err != nil {
		return 0, fmt.Errorf("vulkan: %w", err)
	}
	g.descUsed++
	return set, nil
}

// SetVertices copies the batch's vertices and indices into buffers the
// following draws read (freed after the batch).
func (g *Graphics) SetVertices(vertices []float32, indices []uint32) error {
	if len(vertices) == 0 || len(indices) == 0 {
		return nil
	}
	vb, err := g.newHostBuffer(uint64(4*len(vertices)), vk.BufferUsageVertexBufferBit)
	if err != nil {
		return err
	}
	ib, err := g.newHostBuffer(uint64(4*len(indices)), vk.BufferUsageIndexBufferBit)
	if err != nil {
		g.freeHostBuffer(vb)
		return err
	}
	copy(vb.mapped, unsafe.Slice((*byte)(unsafe.Pointer(&vertices[0])), 4*len(vertices)))
	copy(ib.mapped, unsafe.Slice((*byte)(unsafe.Pointer(&indices[0])), 4*len(indices)))
	g.retire(vb, ib)
	g.vertexBuf, g.indexBuf = vb, ib
	return nil
}

// retire frees the buffers after the batch that may use them.
func (g *Graphics) retire(bufs ...*hostBuffer) {
	g.afterBatch = append(g.afterBatch, func() {
		for _, b := range bufs {
			g.freeHostBuffer(b)
		}
	})
}

func (i *Image) bounds() image.Rectangle { return image.Rect(0, 0, i.width, i.height) }

func (i *Image) imageView() (vk.ImageView, error) {
	if i.view == 0 {
		v, err := i.g.device.CreateImageView2D(i.image, vk.FormatR8G8B8A8Unorm)
		if err != nil {
			return 0, fmt.Errorf("vulkan: %w", err)
		}
		i.view = v
	}
	return i.view, nil
}

// DrawTriangles draws the indexed triangles from indexOffset, a region at a
// time (each its scissor), into dst with the shader, the sources and the
// uniforms (Ebitengine's preserved ones first; std140, no Y flip — Vulkan's
// framebuffer and NDC agree), blended.
func (g *Graphics) DrawTriangles(dstID graphicsdriver.ImageID, srcIDs [graphics.ShaderSrcImageCount]graphicsdriver.ImageID, shaderID graphicsdriver.ShaderID, dstRegions []graphicsdriver.DstRegion, indexOffset int, blend graphicsdriver.Blend, uniforms []uint32) error {
	dst := g.images[dstID]
	shader := g.shaders[shaderID]
	if dst == nil || shader == nil {
		return errors.New("vulkan: DrawTriangles: no such image or shader")
	}
	if g.vertexBuf == nil {
		return errors.New("vulkan: DrawTriangles before SetVertices")
	}
	pipeline, err := shader.pipeline(blend)
	if err != nil {
		return err
	}
	packed, err := spirv.PackStd140(nil, shader.program.Uniforms, uniforms, -1)
	if err != nil {
		return fmt.Errorf("vulkan: %w", err)
	}
	if len(packed) < 4 {
		packed = append(packed, make([]uint32, 4-len(packed))...)
	}
	ub, err := g.newHostBuffer(uint64(4*len(packed)), vk.BufferUsageUniformBufferBit)
	if err != nil {
		return err
	}
	g.retire(ub)
	copy(ub.mapped, unsafe.Slice((*byte)(unsafe.Pointer(&packed[0])), 4*len(packed)))

	cb, err := g.commandBuffer()
	if err != nil {
		return err
	}
	set, err := g.descriptorSet()
	if err != nil {
		return err
	}
	g.device.WriteUniformBuffer(set, 0, ub.buf, 0, uint64(4*len(packed)))
	for k, id := range srcIDs {
		src := g.images[id]
		if src == nil {
			src = g.emptyImage
		}
		if src == dst {
			return errors.New("vulkan: DrawTriangles: an image drawn onto itself")
		}
		if src.layout == vk.ImageLayoutUndefined { // never written: transparent black
			src.transition(cb, vk.ImageLayoutTransferDstOptimal)
			g.device.CmdClearColorImage(cb, src.image, vk.ImageLayoutTransferDstOptimal, [4]float32{})
		}
		src.transition(cb, vk.ImageLayoutShaderReadOnlyOptimal)
		view, err := src.imageView()
		if err != nil {
			return err
		}
		g.device.WriteCombinedImageSampler(set, uint32(1+k), g.sampler, view, vk.ImageLayoutShaderReadOnlyOptimal)
	}
	if dst.layout == vk.ImageLayoutUndefined {
		dst.transition(cb, vk.ImageLayoutTransferDstOptimal)
		g.device.CmdClearColorImage(cb, dst.image, vk.ImageLayoutTransferDstOptimal, [4]float32{})
	}
	dst.transition(cb, vk.ImageLayoutColorAttachmentOptimal)
	view, err := dst.imageView()
	if err != nil {
		return err
	}
	full := vk.Rect2D{Extent: vk.Extent2D{Width: uint32(dst.width), Height: uint32(dst.height)}}
	g.device.CmdBeginRenderingLoad(cb, view, full)
	g.device.CmdBindGraphicsPipeline(cb, pipeline)
	g.device.CmdSetViewport(cb, vk.Viewport{Width: float32(dst.width), Height: float32(dst.height), MaxDepth: 1})
	g.device.CmdBindVertexBuffer(cb, g.vertexBuf.buf, 0)
	g.device.CmdBindIndexBuffer(cb, g.indexBuf.buf, 0, vk.IndexTypeUint32)
	g.device.CmdBindDescriptorSet(cb, g.pipelineLayout, set, 0)
	for _, r := range dstRegions {
		rg := r.Region.Intersect(dst.bounds())
		if !rg.Empty() {
			g.device.CmdSetScissor(cb, vk.Rect2D{Offset: vk.Offset2D{X: int32(rg.Min.X), Y: int32(rg.Min.Y)}, Extent: vk.Extent2D{Width: uint32(rg.Dx()), Height: uint32(rg.Dy())}})
			g.device.CmdDrawIndexed(cb, uint32(r.IndexCount), 1, uint32(indexOffset), 0, 0)
		}
		indexOffset += r.IndexCount
	}
	g.device.CmdEndRendering(cb)
	return nil
}
