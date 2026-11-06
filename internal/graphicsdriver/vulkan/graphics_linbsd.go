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
	"os"

	"github.com/hajimehoshi/ebiten/v2/internal/color"
	"github.com/hajimehoshi/ebiten/v2/internal/graphicsdriver"
	"github.com/hajimehoshi/ebiten/v2/internal/graphicsdriver/vulkan/vk"
)

// debugCallback, when set (the tests), turns on the Khronos validation layer
// and receives its messages.
var debugCallback func(vk.DebugMessage)

// Graphics is the driver: up to framesInFlight batches on the device while
// the next is recorded (End submits without waiting). PARTIAL: a dedicated
// allocation per image; layout transitions with
// all-commands barriers; new host buffers per batch for vertices, indices
// and uniforms (a ring later). The screen (V5) is not here yet.
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
	batches    [framesInFlight]batch
	cur        int // the batch being (or next to be) recorded
	recording  bool
	serial     uint64 // the serial of the batch being recorded
	completed  uint64 // the last batch the device has finished
	afterBatch []func()

	images      map[graphicsdriver.ImageID]*Image
	nextImageID graphicsdriver.ImageID

	// Drawing (draw_linbsd.go).
	shaders        map[graphicsdriver.ShaderID]*Shader
	nextShaderID   graphicsdriver.ShaderID
	setLayout      vk.DescriptorSetLayout
	pipelineLayout vk.PipelineLayout
	sampler        vk.Sampler
	emptyImage     *Image
	descPool       vk.DescriptorPool
	descUsed       int
	vertexBuf      *hostBuffer
	indexBuf       *hostBuffer
}

// NewGraphics makes the driver; Initialize opens the device.
func NewGraphics(colorSpace color.ColorSpace) (graphicsdriver.Graphics, error) {
	return &Graphics{colorSpace: colorSpace, images: map[graphicsdriver.ImageID]*Image{}, shaders: map[graphicsdriver.ShaderID]*Shader{}, serial: 1}, nil
}

// Initialize opens a Vulkan 1.3 instance and the first device with a
// graphics queue, with dynamic rendering and synchronization2.
func (g *Graphics) Initialize() error {
	if v, err := vk.EnumerateInstanceVersion(); err != nil || v < vk.APIVersion13 {
		return fmt.Errorf("vulkan: no Vulkan 1.3 loader (%v)", err)
	}
	if debugCallback == nil && os.Getenv("EBITENGINE_VULKAN_VALIDATION") == "1" {
		// The validation layer for a game or Ebitengine's tests: its messages on stderr.
		debugCallback = func(m vk.DebugMessage) { fmt.Fprintf(os.Stderr, "vulkan validation: %s: %s\n", m.ID, m.Text) }
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
	for i := range g.batches {
		b := &g.batches[i]
		if b.cb, err = g.device.AllocateCommandBuffer(g.pool); err != nil {
			return fmt.Errorf("vulkan: %w", err)
		}
		if b.fence, err = g.device.CreateFence(false); err != nil {
			return fmt.Errorf("vulkan: %w", err)
		}
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
		g.destroyDrawing()
		_ = g.device.WaitIdle()
		g.retireUpTo(g.serial, true)
		g.runAfterBatch()
		for i := range g.batches {
			g.device.DestroyFence(g.batches[i].fence)
		}
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

// framesInFlight is how many batches may be submitted and not yet done:
// the CPU records the next while the device runs the ones before.
const framesInFlight = 3

// batch is a command buffer submitted with its fence, its serial and what
// to release once the device is done with it.
type batch struct {
	cb       vk.CommandBuffer
	fence    vk.Fence
	serial   uint64
	after    []func()
	inFlight bool
}

// commandBuffer is the command buffer being recorded, begun if none is —
// in the next batch of the ring, waiting first for the device to finish
// the batch last recorded in it.
func (g *Graphics) commandBuffer() (vk.CommandBuffer, error) {
	b := &g.batches[g.cur]
	if !g.recording {
		if b.inFlight {
			if err := g.retire(b); err != nil {
				return 0, err
			}
		}
		if err := g.device.ResetFence(b.fence); err != nil {
			return 0, fmt.Errorf("vulkan: %w", err)
		}
		if err := g.device.BeginCommandBuffer(b.cb, vk.CommandBufferUsageOneTimeSubmitBit); err != nil {
			return 0, fmt.Errorf("vulkan: %w", err)
		}
		g.recording = true
	}
	return b.cb, nil
}

// flush submits what was recorded with its batch's fence — without
// waiting: the batch's resources are released and its read-backs complete
// when the fence is seen signaled (retire).
func (g *Graphics) flush() error {
	if !g.recording {
		return nil
	}
	g.recording = false
	b := &g.batches[g.cur]
	if err := g.device.EndCommandBuffer(b.cb); err != nil {
		return fmt.Errorf("vulkan: %w", err)
	}
	if err := g.device.QueueSubmitFence(g.queue, b.fence, b.cb); err != nil {
		return fmt.Errorf("vulkan: %w", err)
	}
	b.serial, b.after, b.inFlight = g.serial, g.afterBatch, true
	g.afterBatch = nil
	g.serial++
	g.cur = (g.cur + 1) % framesInFlight
	return nil
}

// retire waits for the batch's fence and releases what it used.
func (g *Graphics) retire(b *batch) error {
	if err := g.device.WaitForFence(b.fence); err != nil {
		return fmt.Errorf("vulkan: %w", err)
	}
	for _, f := range b.after {
		f()
	}
	b.after, b.inFlight = nil, false
	g.completed = max(g.completed, b.serial)
	return nil
}

// retireUpTo retires the batches in flight up to serial, oldest first —
// waiting for them when wait, else only those whose fences are signaled
// (stopping at the first that isn't). Whether serial has completed.
func (g *Graphics) retireUpTo(serial uint64, wait bool) bool {
	for {
		var oldest *batch
		for i := range g.batches {
			if b := &g.batches[i]; b.inFlight && b.serial <= serial && (oldest == nil || b.serial < oldest.serial) {
				oldest = b
			}
		}
		if oldest == nil {
			return g.completed >= serial
		}
		if !wait {
			if done, err := g.device.FenceSignaled(oldest.fence); err != nil || !done {
				return false
			}
		}
		if err := g.retire(oldest); err != nil {
			return false
		}
	}
}

// release runs free once the device can no longer be using what it frees:
// after the batch being recorded, else after the newest one in flight,
// else now.
func (g *Graphics) release(free func()) {
	if g.recording {
		g.afterBatch = append(g.afterBatch, free)
		return
	}
	var newest *batch
	for i := range g.batches {
		if b := &g.batches[i]; b.inFlight && (newest == nil || b.serial > newest.serial) {
			newest = b
		}
	}
	if newest != nil {
		newest.after = append(newest.after, free)
		return
	}
	free()
}

func (g *Graphics) runAfterBatch() {
	for _, f := range g.afterBatch {
		f()
	}
	g.afterBatch = nil
}

func (g *Graphics) SetTransparent(transparent bool) {}

// NewScreenFramebufferImage is, until the swapchain (V5), an offscreen image:
// games run and draw, nothing is presented.
func (g *Graphics) NewScreenFramebufferImage(width, height int) (graphicsdriver.Image, error) {
	return g.newImage(width, height) // the screen: its exact size
}

func (g *Graphics) SetVsyncEnabled(enabled bool) {}

func (g *Graphics) NeedsClearingScreen() bool { return true }

func (g *Graphics) MaxImageSize() int { return g.maxImage }

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
