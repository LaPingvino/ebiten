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

// Package vulkan is Ebitengine's Vulkan graphics driver (purego, no cgo):
// see docs/vulkan/DESIGN.md.
package vulkan

import (
	"errors"
	"fmt"

	"github.com/hajimehoshi/ebiten/v2/internal/color"
	"github.com/hajimehoshi/ebiten/v2/internal/graphics"
	"github.com/hajimehoshi/ebiten/v2/internal/graphicsdriver"
	"github.com/hajimehoshi/ebiten/v2/internal/graphicsdriver/vulkan/vk"
	"github.com/hajimehoshi/ebiten/v2/internal/shaderir"
)

// debugCallback, when set (the tests), turns on the Khronos validation layer
// and receives its messages.
var debugCallback func(vk.DebugMessage)

// Graphics is the driver. PARTIAL (V4, step 1): one command buffer at a
// time — End submits it and waits for the device (no frames in flight
// yet); a dedicated allocation per image; layout transitions with
// all-commands barriers. Drawing (NewShader, DrawTriangles) and the screen
// (V5) are not here yet.
type Graphics struct {
	colorSpace color.ColorSpace

	instance  vk.Instance
	messenger vk.DebugUtilsMessengerEXT
	physical  vk.PhysicalDevice
	family    uint32
	device    *vk.Device
	queue     vk.Queue
	memProps  vk.PhysicalDeviceMemoryProperties
	maxImage  int

	pool       vk.CommandPool
	cb         vk.CommandBuffer
	recording  bool
	serial     uint64 // the serial of the batch being recorded
	completed  uint64 // the last batch the device has finished
	afterBatch []func()

	images      map[graphicsdriver.ImageID]*Image
	nextImageID graphicsdriver.ImageID
}

// NewGraphics makes the driver; Initialize opens the device.
func NewGraphics(colorSpace color.ColorSpace) (graphicsdriver.Graphics, error) {
	return &Graphics{colorSpace: colorSpace, images: map[graphicsdriver.ImageID]*Image{}, serial: 1}, nil
}

// Initialize opens a Vulkan 1.3 instance and the first device with a
// graphics queue, with dynamic rendering and synchronization2.
func (g *Graphics) Initialize() error {
	if v, err := vk.EnumerateInstanceVersion(); err != nil || v < vk.APIVersion13 {
		return fmt.Errorf("vulkan: no Vulkan 1.3 loader (%v)", err)
	}
	var exts, layers []string
	if debugCallback != nil {
		exts, layers = []string{vk.ExtDebugUtils}, []string{vk.LayerKhronosValidation}
	}
	instance, err := vk.CreateInstance("ebitengine", vk.APIVersion13, exts, layers)
	if err != nil && debugCallback != nil && (errors.Is(err, vk.ErrorLayerNotPresent) || errors.Is(err, vk.ErrorExtensionNotPresent)) {
		exts, layers = nil, nil
		instance, err = vk.CreateInstance("ebitengine", vk.APIVersion13, nil, nil)
	}
	if err != nil {
		return fmt.Errorf("vulkan: %w", err)
	}
	g.instance = instance
	if layers != nil {
		if g.messenger, err = vk.CreateDebugUtilsMessenger(instance,
			vk.DebugUtilsMessageSeverityWarningBitEXT|vk.DebugUtilsMessageSeverityErrorBitEXT,
			vk.DebugUtilsMessageTypeGeneralBitEXT|vk.DebugUtilsMessageTypeValidationBitEXT|vk.DebugUtilsMessageTypePerformanceBitEXT,
			debugCallback); err != nil {
			return fmt.Errorf("vulkan: %w", err)
		}
	}
	devices, err := vk.EnumeratePhysicalDevices(instance)
	if err != nil {
		return fmt.Errorf("vulkan: %w", err)
	}
	found := false
	for _, d := range devices {
		props := vk.GetPhysicalDeviceProperties(instance, d)
		if props.APIVersion < vk.APIVersion13 {
			continue
		}
		for i, f := range vk.GetPhysicalDeviceQueueFamilyProperties(instance, d) {
			if f.QueueFlags&vk.QueueGraphicsBit != 0 && f.QueueCount > 0 {
				g.physical, g.family, g.maxImage, found = d, uint32(i), int(props.MaxImageDimension2D), true
				break
			}
		}
		if found {
			break
		}
	}
	if !found {
		return errors.New("vulkan: no Vulkan 1.3 device with a graphics queue")
	}
	if g.device, err = vk.CreateDevice(instance, g.physical, g.family, &vk.PhysicalDeviceVulkan13Features{Synchronization2: 1, DynamicRendering: 1}, nil); err != nil {
		return fmt.Errorf("vulkan: %w", err)
	}
	g.queue = g.device.Queue(g.family, 0)
	g.memProps = vk.GetPhysicalDeviceMemoryProperties(instance, g.physical)
	const resetCommandBuffer = 0x2 // VK_COMMAND_POOL_CREATE_RESET_COMMAND_BUFFER_BIT
	if g.pool, err = g.device.CreateCommandPool(g.family, resetCommandBuffer); err != nil {
		return fmt.Errorf("vulkan: %w", err)
	}
	if g.cb, err = g.device.AllocateCommandBuffer(g.pool); err != nil {
		return fmt.Errorf("vulkan: %w", err)
	}
	return nil
}

// destroy releases everything (the tests; a game keeps its driver).
func (g *Graphics) destroy() {
	if g.device != nil {
		_ = g.flush()
		for _, img := range g.images {
			img.Dispose()
		}
		g.runAfterBatch()
		g.device.DestroyCommandPool(g.pool)
		g.device.Destroy()
		g.device = nil
	}
	if g.messenger != 0 {
		vk.DestroyDebugUtilsMessenger(g.instance, g.messenger)
		g.messenger = 0
	}
	if g.instance != 0 {
		vk.DestroyInstance(g.instance)
		g.instance = 0
	}
}

func (g *Graphics) ColorSpace() color.ColorSpace { return g.colorSpace }

func (g *Graphics) Begin() error { return nil }

func (g *Graphics) End(mode graphicsdriver.FlushMode) error { return g.flush() }

// commandBuffer is the command buffer being recorded, begun if none is.
func (g *Graphics) commandBuffer() (vk.CommandBuffer, error) {
	if !g.recording {
		if err := g.device.BeginCommandBuffer(g.cb, vk.CommandBufferUsageOneTimeSubmitBit); err != nil {
			return 0, fmt.Errorf("vulkan: %w", err)
		}
		g.recording = true
	}
	return g.cb, nil
}

// flush submits what was recorded and waits for the device; the batch's
// staging resources are released and its read-backs complete.
func (g *Graphics) flush() error {
	if !g.recording {
		return nil
	}
	g.recording = false
	if err := g.device.EndCommandBuffer(g.cb); err != nil {
		return fmt.Errorf("vulkan: %w", err)
	}
	if err := g.device.QueueSubmit(g.queue, g.cb); err != nil {
		return fmt.Errorf("vulkan: %w", err)
	}
	if err := g.device.QueueWaitIdle(g.queue); err != nil {
		return fmt.Errorf("vulkan: %w", err)
	}
	g.completed = g.serial
	g.serial++
	g.runAfterBatch()
	return nil
}

func (g *Graphics) runAfterBatch() {
	for _, f := range g.afterBatch {
		f()
	}
	g.afterBatch = nil
}

func (g *Graphics) SetTransparent(transparent bool) {}

func (g *Graphics) SetVertices(vertices []float32, indices []uint32) error {
	return errors.New("vulkan: SetVertices not implemented yet")
}

func (g *Graphics) NewScreenFramebufferImage(width, height int) (graphicsdriver.Image, error) {
	return nil, errors.New("vulkan: the screen is not implemented yet (V5)")
}

func (g *Graphics) SetVsyncEnabled(enabled bool) {}

func (g *Graphics) NeedsClearingScreen() bool { return true }

func (g *Graphics) MaxImageSize() int { return g.maxImage }

func (g *Graphics) NewShader(program *shaderir.Program) (graphicsdriver.Shader, error) {
	return nil, errors.New("vulkan: NewShader not implemented yet")
}

func (g *Graphics) DrawTriangles(dst graphicsdriver.ImageID, srcs [graphics.ShaderSrcImageCount]graphicsdriver.ImageID, shader graphicsdriver.ShaderID, dstRegions []graphicsdriver.DstRegion, indexOffset int, blend graphicsdriver.Blend, uniforms []uint32) error {
	return errors.New("vulkan: DrawTriangles not implemented yet")
}

// hostBuffer is a host-visible, coherent buffer, mapped.
type hostBuffer struct {
	buf    vk.Buffer
	mem    vk.DeviceMemory
	mapped []byte
}

func (g *Graphics) newHostBuffer(size uint64, usage vk.BufferUsageFlags) (*hostBuffer, error) {
	buf, err := g.device.CreateBuffer(size, usage)
	if err != nil {
		return nil, fmt.Errorf("vulkan: %w", err)
	}
	req := g.device.BufferMemoryRequirements(buf)
	typ, ok := g.memProps.FindMemoryType(req.MemoryTypeBits, vk.MemoryPropertyHostVisibleBit|vk.MemoryPropertyHostCoherentBit)
	if !ok {
		g.device.DestroyBuffer(buf)
		return nil, errors.New("vulkan: no host-visible, coherent memory")
	}
	mem, err := g.device.AllocateMemory(req.Size, typ)
	if err != nil {
		g.device.DestroyBuffer(buf)
		return nil, fmt.Errorf("vulkan: %w", err)
	}
	if err := g.device.BindBufferMemory(buf, mem, 0); err != nil {
		g.device.FreeMemory(mem)
		g.device.DestroyBuffer(buf)
		return nil, fmt.Errorf("vulkan: %w", err)
	}
	mapped, err := g.device.MapMemory(mem, 0, size)
	if err != nil {
		g.device.FreeMemory(mem)
		g.device.DestroyBuffer(buf)
		return nil, fmt.Errorf("vulkan: %w", err)
	}
	return &hostBuffer{buf, mem, mapped}, nil
}

func (g *Graphics) freeHostBuffer(b *hostBuffer) {
	g.device.UnmapMemory(b.mem)
	g.device.DestroyBuffer(b.buf)
	g.device.FreeMemory(b.mem)
}
