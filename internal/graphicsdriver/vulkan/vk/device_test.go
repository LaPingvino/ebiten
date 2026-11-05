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

package vk_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/hajimehoshi/ebiten/v2/internal/graphicsdriver/vulkan/vk"
	"github.com/hajimehoshi/ebiten/v2/internal/shaderir/spirv"
)

// testInstance is a Vulkan 1.3 instance and its first device with a
// graphics queue, or a skip where there is none.
func testInstance(t *testing.T) (vk.Instance, vk.PhysicalDevice, uint32) {
	t.Helper()
	if v, err := vk.EnumerateInstanceVersion(); err != nil || v < vk.APIVersion13 {
		t.Skipf("no Vulkan 1.3 loader (%v)", err)
	}
	instance, err := vk.CreateInstance("ebitengine-test", vk.APIVersion13, nil)
	if errors.Is(err, vk.ErrorIncompatibleDriver) {
		t.Skip("no Vulkan 1.3 driver installed")
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { vk.DestroyInstance(instance) })
	devices, err := vk.EnumeratePhysicalDevices(instance)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range devices {
		if vk.GetPhysicalDeviceProperties(instance, d).APIVersion < vk.APIVersion13 {
			continue
		}
		for i, f := range vk.GetPhysicalDeviceQueueFamilyProperties(instance, d) {
			if f.QueueFlags&vk.QueueGraphicsBit != 0 && f.QueueCount > 0 {
				return instance, d, uint32(i)
			}
		}
	}
	t.Skip("no Vulkan 1.3 device with a graphics queue")
	return 0, 0, 0
}

// The device the driver needs: dynamic rendering and synchronization2
// supported, a logical device made with them, its queue, idle, destroyed.
func TestDevice(t *testing.T) {
	instance, physical, family := testInstance(t)
	f, err := vk.GetPhysicalDeviceVulkan13Features(instance, physical)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("1.3 features: dynamicRendering %d, synchronization2 %d, maintenance4 %d", f.DynamicRendering, f.Synchronization2, f.Maintenance4)
	if f.DynamicRendering == 0 || f.Synchronization2 == 0 {
		t.Fatal("a Vulkan 1.3 device must support dynamicRendering and synchronization2")
	}
	device, err := vk.CreateDevice(instance, physical, family, &vk.PhysicalDeviceVulkan13Features{DynamicRendering: 1, Synchronization2: 1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer device.Destroy()
	if q := device.Queue(family, 0); q == 0 {
		t.Error("no queue")
	}
	if err := device.WaitIdle(); err != nil {
		t.Error(err)
	}
}

// An unknown device extension is refused with VK_ERROR_EXTENSION_NOT_PRESENT.
func TestDeviceMissingExtension(t *testing.T) {
	instance, physical, family := testInstance(t)
	_, err := vk.CreateDevice(instance, physical, family, nil, []string{"VK_EBITENGINE_no_such_extension"})
	if !errors.Is(err, vk.ErrorExtensionNotPresent) {
		t.Fatalf("got %v, want VK_ERROR_EXTENSION_NOT_PRESENT", err)
	}
}

// A host-visible buffer: made, its memory found, allocated, bound, written
// through one mapping and read back through another.
func TestBufferRoundTrip(t *testing.T) {
	instance, physical, family := testInstance(t)
	device, err := vk.CreateDevice(instance, physical, family, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer device.Destroy()

	const size = 256
	buf, err := device.CreateBuffer(size, vk.BufferUsageTransferSrcBit|vk.BufferUsageVertexBufferBit)
	if err != nil {
		t.Fatal(err)
	}
	defer device.DestroyBuffer(buf)
	req := device.BufferMemoryRequirements(buf)
	if req.Size < size || req.MemoryTypeBits == 0 {
		t.Fatalf("requirements %+v", req)
	}
	props := vk.GetPhysicalDeviceMemoryProperties(instance, physical)
	typ, ok := props.FindMemoryType(req.MemoryTypeBits, vk.MemoryPropertyHostVisibleBit|vk.MemoryPropertyHostCoherentBit)
	if !ok {
		t.Fatalf("no host-visible, coherent memory among %d types", props.MemoryTypeCount)
	}
	t.Logf("%d memory types, %d heaps; buffer: %d bytes, alignment %d, type %d", props.MemoryTypeCount, props.MemoryHeapCount, req.Size, req.Alignment, typ)
	mem, err := device.AllocateMemory(req.Size, typ)
	if err != nil {
		t.Fatal(err)
	}
	defer device.FreeMemory(mem)
	if err := device.BindBufferMemory(buf, mem, 0); err != nil {
		t.Fatal(err)
	}

	b, err := device.MapMemory(mem, 0, size)
	if err != nil {
		t.Fatal(err)
	}
	for i := range b {
		b[i] = byte(i * 7)
	}
	device.UnmapMemory(mem)
	b, err = device.MapMemory(mem, 0, size)
	if err != nil {
		t.Fatal(err)
	}
	defer device.UnmapMemory(mem)
	for i := range b {
		if b[i] != byte(i*7) {
			t.Fatalf("byte %d: %d, want %d", i, b[i], byte(i*7))
		}
	}
}

// The first command the device runs: vkCmdFillBuffer recorded into a command
// buffer, submitted, waited for; the buffer then holds the word everywhere
// the command wrote and the host's own bytes elsewhere.
func TestFillBufferOnTheDevice(t *testing.T) {
	instance, physical, family := testInstance(t)
	device, err := vk.CreateDevice(instance, physical, family, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer device.Destroy()
	queue := device.Queue(family, 0)

	const size = 1024
	buf, err := device.CreateBuffer(size, vk.BufferUsageTransferDstBit)
	if err != nil {
		t.Fatal(err)
	}
	defer device.DestroyBuffer(buf)
	req := device.BufferMemoryRequirements(buf)
	props := vk.GetPhysicalDeviceMemoryProperties(instance, physical)
	typ, ok := props.FindMemoryType(req.MemoryTypeBits, vk.MemoryPropertyHostVisibleBit|vk.MemoryPropertyHostCoherentBit)
	if !ok {
		t.Skip("no host-visible, coherent memory")
	}
	mem, err := device.AllocateMemory(req.Size, typ)
	if err != nil {
		t.Fatal(err)
	}
	defer device.FreeMemory(mem)
	if err := device.BindBufferMemory(buf, mem, 0); err != nil {
		t.Fatal(err)
	}
	b, err := device.MapMemory(mem, 0, size)
	if err != nil {
		t.Fatal(err)
	}
	defer device.UnmapMemory(mem)
	for i := range b {
		b[i] = 0x11
	}

	pool, err := device.CreateCommandPool(family, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer device.DestroyCommandPool(pool)
	cb, err := device.AllocateCommandBuffer(pool)
	if err != nil {
		t.Fatal(err)
	}
	if err := device.BeginCommandBuffer(cb, vk.CommandBufferUsageOneTimeSubmitBit); err != nil {
		t.Fatal(err)
	}
	device.CmdFillBuffer(cb, buf, 256, 512, 0xCAFEF00D)
	if err := device.EndCommandBuffer(cb); err != nil {
		t.Fatal(err)
	}
	if err := device.QueueSubmit(queue, cb); err != nil {
		t.Fatal(err)
	}
	if err := device.QueueWaitIdle(queue); err != nil {
		t.Fatal(err)
	}

	for i := range b {
		want := byte(0x11)
		if i >= 256 && i < 768 {
			want = [4]byte{0x0D, 0xF0, 0xFE, 0xCA}[i%4] // the word, little-endian
		}
		if b[i] != want {
			t.Fatalf("byte %d: %#x, want %#x", i, b[i], want)
		}
	}
}

// The first image: 4×3 RGBA, moved into transfer layout with a
// synchronization2 barrier, cleared on the device to a colour, copied to a
// host-visible buffer and read: every pixel the colour, as 8-bit RGBA.
func TestClearImageAndReadBack(t *testing.T) {
	instance, physical, family := testInstance(t)
	device, err := vk.CreateDevice(instance, physical, family, &vk.PhysicalDeviceVulkan13Features{Synchronization2: 1, DynamicRendering: 1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer device.Destroy()
	queue := device.Queue(family, 0)
	props := vk.GetPhysicalDeviceMemoryProperties(instance, physical)

	const w, h = 4, 3
	img, err := device.CreateImage2D(w, h, vk.FormatR8G8B8A8Unorm, vk.ImageUsageTransferSrcBit|vk.ImageUsageTransferDstBit)
	if err != nil {
		t.Fatal(err)
	}
	defer device.DestroyImage(img)
	ireq := device.ImageMemoryRequirements(img)
	ityp, ok := props.FindMemoryType(ireq.MemoryTypeBits, 0)
	if !ok {
		t.Fatal("no memory type for the image")
	}
	imem, err := device.AllocateMemory(ireq.Size, ityp)
	if err != nil {
		t.Fatal(err)
	}
	defer device.FreeMemory(imem)
	if err := device.BindImageMemory(img, imem, 0); err != nil {
		t.Fatal(err)
	}

	buf, err := device.CreateBuffer(w*h*4, vk.BufferUsageTransferDstBit)
	if err != nil {
		t.Fatal(err)
	}
	defer device.DestroyBuffer(buf)
	breq := device.BufferMemoryRequirements(buf)
	btyp, ok := props.FindMemoryType(breq.MemoryTypeBits, vk.MemoryPropertyHostVisibleBit|vk.MemoryPropertyHostCoherentBit)
	if !ok {
		t.Skip("no host-visible, coherent memory")
	}
	bmem, err := device.AllocateMemory(breq.Size, btyp)
	if err != nil {
		t.Fatal(err)
	}
	defer device.FreeMemory(bmem)
	if err := device.BindBufferMemory(buf, bmem, 0); err != nil {
		t.Fatal(err)
	}

	pool, err := device.CreateCommandPool(family, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer device.DestroyCommandPool(pool)
	cb, err := device.AllocateCommandBuffer(pool)
	if err != nil {
		t.Fatal(err)
	}
	if err := device.BeginCommandBuffer(cb, vk.CommandBufferUsageOneTimeSubmitBit); err != nil {
		t.Fatal(err)
	}
	device.CmdImageBarrier(cb, vk.ImageMemoryBarrier2{
		DstStageMask: vk.PipelineStage2AllTransfer, DstAccessMask: vk.Access2TransferWrite,
		OldLayout: vk.ImageLayoutUndefined, NewLayout: vk.ImageLayoutTransferDstOptimal,
		SrcQueueFamilyIndex: vk.QueueFamilyIgnored, DstQueueFamilyIndex: vk.QueueFamilyIgnored,
		Image: img, SubresourceRange: vk.ColorRange,
	})
	device.CmdClearColorImage(cb, img, vk.ImageLayoutTransferDstOptimal, [4]float32{1, 0.5, 0, 1})
	device.CmdImageBarrier(cb, vk.ImageMemoryBarrier2{
		SrcStageMask: vk.PipelineStage2AllTransfer, SrcAccessMask: vk.Access2TransferWrite,
		DstStageMask: vk.PipelineStage2AllTransfer, DstAccessMask: vk.Access2TransferRead,
		OldLayout: vk.ImageLayoutTransferDstOptimal, NewLayout: vk.ImageLayoutTransferSrcOptimal,
		SrcQueueFamilyIndex: vk.QueueFamilyIgnored, DstQueueFamilyIndex: vk.QueueFamilyIgnored,
		Image: img, SubresourceRange: vk.ColorRange,
	})
	device.CmdCopyImageToBuffer(cb, img, vk.ImageLayoutTransferSrcOptimal, buf, vk.BufferImageCopy{
		ImageSubresource: vk.ImageSubresourceLayers{AspectMask: vk.ImageAspectColorBit, LayerCount: 1},
		ImageExtent:      vk.Extent3D{Width: w, Height: h, Depth: 1},
	})
	if err := device.EndCommandBuffer(cb); err != nil {
		t.Fatal(err)
	}
	if err := device.QueueSubmit(queue, cb); err != nil {
		t.Fatal(err)
	}
	if err := device.QueueWaitIdle(queue); err != nil {
		t.Fatal(err)
	}

	px, err := device.MapMemory(bmem, 0, w*h*4)
	if err != nil {
		t.Fatal(err)
	}
	defer device.UnmapMemory(bmem)
	for i := 0; i < w*h; i++ {
		r, g, b, a := px[i*4], px[i*4+1], px[i*4+2], px[i*4+3]
		if r != 255 || (g != 127 && g != 128) || b != 0 || a != 255 {
			t.Fatalf("pixel %d: %d %d %d %d, want 255 127|128 0 255", i, r, g, b, a)
		}
	}
	t.Logf("%d×%d pixels cleared on the device, read back: %v", w, h, px[:4])
}

// A shader module the SPIR-V backend writes is taken by the driver.
func TestShaderModuleFromSPIRV(t *testing.T) {
	instance, physical, family := testInstance(t)
	device, err := vk.CreateDevice(instance, physical, family, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer device.Destroy()
	m := spirv.NewModule()
	m.Capability(spirv.CapabilityShader)
	m.MemoryModel(spirv.AddressingLogical, spirv.MemoryModelGLSL450)
	void := m.TypeVoid()
	main := m.Function(void, m.TypeFunction(void), func(f *spirv.Function) {
		f.Label()
		f.Return()
	})
	m.EntryPoint(spirv.ExecutionModelFragment, main, "main")
	m.ExecutionMode(main, spirv.ExecutionModeOriginUpperLeft)
	sm, err := device.CreateShaderModule(m.Words())
	if err != nil {
		t.Fatal(err)
	}
	if sm == 0 {
		t.Error("no shader module")
	}
	device.DestroyShaderModule(sm)
}

// shaders is the backend's constant-colour fragment shader and a vertex
// shader that puts every vertex at one point (a draw that covers nothing).
func shaders(t *testing.T, device *vk.Device, rgba [4]float32, scale float32) (vk.ShaderModule, vk.ShaderModule) {
	t.Helper()
	frag := spirv.NewModule()
	frag.Capability(spirv.CapabilityShader)
	frag.MemoryModel(spirv.AddressingLogical, spirv.MemoryModelGLSL450)
	vec4 := frag.TypeVector(frag.TypeFloat(32), 4)
	out := frag.Variable(frag.TypePointer(spirv.StorageClassOutput, vec4), spirv.StorageClassOutput)
	frag.Decorate(out, spirv.DecorationLocation, 0)
	colour := frag.ConstantComposite(vec4, frag.ConstantFloat32(rgba[0]), frag.ConstantFloat32(rgba[1]), frag.ConstantFloat32(rgba[2]), frag.ConstantFloat32(rgba[3]))
	void := frag.TypeVoid()
	fmain := frag.Function(void, frag.TypeFunction(void), func(f *spirv.Function) {
		f.Label()
		f.Store(out, colour)
		f.Return()
	})
	frag.EntryPoint(spirv.ExecutionModelFragment, fmain, "main", out)
	frag.ExecutionMode(fmain, spirv.ExecutionModeOriginUpperLeft)

	// The vertex shader: positions from gl_VertexIndex, x = float((i << 1) & 2)
	// * scale - 1, y = float(i & 2) * scale - 1 — scale 2 covers the target,
	// 1 the half above its diagonal, 0 nothing (all three at a corner).
	vert := spirv.NewModule()
	vert.Capability(spirv.CapabilityShader)
	vert.MemoryModel(spirv.AddressingLogical, spirv.MemoryModelGLSL450)
	i32, f32 := vert.TypeInt(32, true), vert.TypeFloat(32)
	vvec4 := vert.TypeVector(f32, 4)
	index := vert.Variable(vert.TypePointer(spirv.StorageClassInput, i32), spirv.StorageClassInput)
	vert.Decorate(index, spirv.DecorationBuiltIn, spirv.BuiltInVertexIndex)
	pos := vert.Variable(vert.TypePointer(spirv.StorageClassOutput, vvec4), spirv.StorageClassOutput)
	vert.Decorate(pos, spirv.DecorationBuiltIn, spirv.BuiltInPosition)
	ione, itwo, k := vert.ConstantInt32(1), vert.ConstantInt32(2), vert.ConstantFloat32(scale)
	zero, one := vert.ConstantFloat32(0), vert.ConstantFloat32(1)
	vvoid := vert.TypeVoid()
	vmain := vert.Function(vvoid, vert.TypeFunction(vvoid), func(f *spirv.Function) {
		f.Label()
		i := f.Load(i32, index)
		x := f.FSub(f32, f.FMul(f32, f.ConvertSToF(f32, f.BitwiseAnd(i32, f.ShiftLeftLogical(i32, i, ione), itwo)), k), one)
		y := f.FSub(f32, f.FMul(f32, f.ConvertSToF(f32, f.BitwiseAnd(i32, i, itwo)), k), one)
		f.Store(pos, f.CompositeConstruct(vvec4, x, y, zero, one))
		f.Return()
	})
	vert.EntryPoint(spirv.ExecutionModelVertex, vmain, "main", index, pos)

	vs, err := device.CreateShaderModule(vert.Words())
	if err != nil {
		t.Fatal(err)
	}
	fs, err := device.CreateShaderModule(frag.Words())
	if err != nil {
		t.Fatal(err)
	}
	return vs, fs
}

// Dynamic rendering through a graphics pipeline made of the backend's
// shaders: the attachment cleared by the rendering's load op, the pipeline
// bound, viewport and scissor set, a draw recorded; the image read back.
// draw renders the backend's shaders' triangle (red, at a scale) into a w×h
// RGBA image cleared blue — dynamic rendering through a graphics pipeline —
// and reads the image back.
func draw(t *testing.T, w, h uint32, scale float32) []byte {
	t.Helper()
	instance, physical, family := testInstance(t)
	device, err := vk.CreateDevice(instance, physical, family, &vk.PhysicalDeviceVulkan13Features{Synchronization2: 1, DynamicRendering: 1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer device.Destroy()
	queue := device.Queue(family, 0)
	props := vk.GetPhysicalDeviceMemoryProperties(instance, physical)

	img, err := device.CreateImage2D(w, h, vk.FormatR8G8B8A8Unorm, vk.ImageUsageColorAttachmentBit|vk.ImageUsageTransferSrcBit)
	if err != nil {
		t.Fatal(err)
	}
	defer device.DestroyImage(img)
	ireq := device.ImageMemoryRequirements(img)
	ityp, _ := props.FindMemoryType(ireq.MemoryTypeBits, 0)
	imem, err := device.AllocateMemory(ireq.Size, ityp)
	if err != nil {
		t.Fatal(err)
	}
	defer device.FreeMemory(imem)
	if err := device.BindImageMemory(img, imem, 0); err != nil {
		t.Fatal(err)
	}
	view, err := device.CreateImageView2D(img, vk.FormatR8G8B8A8Unorm)
	if err != nil {
		t.Fatal(err)
	}
	defer device.DestroyImageView(view)

	vs, fs := shaders(t, device, [4]float32{1, 0, 0, 1}, scale)
	defer device.DestroyShaderModule(vs)
	defer device.DestroyShaderModule(fs)
	layout, err := device.CreatePipelineLayout()
	if err != nil {
		t.Fatal(err)
	}
	defer device.DestroyPipelineLayout(layout)
	pipeline, err := device.CreateGraphicsPipeline(layout, vs, fs, vk.FormatR8G8B8A8Unorm)
	if err != nil {
		t.Fatal(err)
	}
	defer device.DestroyPipeline(pipeline)

	buf, err := device.CreateBuffer(uint64(w*h*4), vk.BufferUsageTransferDstBit)
	if err != nil {
		t.Fatal(err)
	}
	defer device.DestroyBuffer(buf)
	breq := device.BufferMemoryRequirements(buf)
	btyp, ok := props.FindMemoryType(breq.MemoryTypeBits, vk.MemoryPropertyHostVisibleBit|vk.MemoryPropertyHostCoherentBit)
	if !ok {
		t.Skip("no host-visible, coherent memory")
	}
	bmem, err := device.AllocateMemory(breq.Size, btyp)
	if err != nil {
		t.Fatal(err)
	}
	defer device.FreeMemory(bmem)
	if err := device.BindBufferMemory(buf, bmem, 0); err != nil {
		t.Fatal(err)
	}

	pool, err := device.CreateCommandPool(family, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer device.DestroyCommandPool(pool)
	cb, err := device.AllocateCommandBuffer(pool)
	if err != nil {
		t.Fatal(err)
	}
	if err := device.BeginCommandBuffer(cb, vk.CommandBufferUsageOneTimeSubmitBit); err != nil {
		t.Fatal(err)
	}
	device.CmdImageBarrier(cb, vk.ImageMemoryBarrier2{
		DstStageMask: vk.PipelineStage2ColorAttachmentOutput, DstAccessMask: vk.Access2ColorAttachmentWrite,
		OldLayout: vk.ImageLayoutUndefined, NewLayout: vk.ImageLayoutColorAttachmentOptimal,
		SrcQueueFamilyIndex: vk.QueueFamilyIgnored, DstQueueFamilyIndex: vk.QueueFamilyIgnored,
		Image: img, SubresourceRange: vk.ColorRange,
	})
	area := vk.Rect2D{Extent: vk.Extent2D{Width: w, Height: h}}
	device.CmdBeginRendering(cb, view, area, [4]float32{0, 0, 1, 1})
	device.CmdBindGraphicsPipeline(cb, pipeline)
	device.CmdSetViewport(cb, vk.Viewport{Width: float32(w), Height: float32(h), MaxDepth: 1})
	device.CmdSetScissor(cb, area)
	device.CmdDraw(cb, 3, 1, 0, 0)
	device.CmdEndRendering(cb)
	device.CmdImageBarrier(cb, vk.ImageMemoryBarrier2{
		SrcStageMask: vk.PipelineStage2ColorAttachmentOutput, SrcAccessMask: vk.Access2ColorAttachmentWrite,
		DstStageMask: vk.PipelineStage2AllTransfer, DstAccessMask: vk.Access2TransferRead,
		OldLayout: vk.ImageLayoutColorAttachmentOptimal, NewLayout: vk.ImageLayoutTransferSrcOptimal,
		SrcQueueFamilyIndex: vk.QueueFamilyIgnored, DstQueueFamilyIndex: vk.QueueFamilyIgnored,
		Image: img, SubresourceRange: vk.ColorRange,
	})
	device.CmdCopyImageToBuffer(cb, img, vk.ImageLayoutTransferSrcOptimal, buf, vk.BufferImageCopy{
		ImageSubresource: vk.ImageSubresourceLayers{AspectMask: vk.ImageAspectColorBit, LayerCount: 1},
		ImageExtent:      vk.Extent3D{Width: w, Height: h, Depth: 1},
	})
	if err := device.EndCommandBuffer(cb); err != nil {
		t.Fatal(err)
	}
	if err := device.QueueSubmit(queue, cb); err != nil {
		t.Fatal(err)
	}
	if err := device.QueueWaitIdle(queue); err != nil {
		t.Fatal(err)
	}
	px, err := device.MapMemory(bmem, 0, uint64(w*h*4))
	if err != nil {
		t.Fatal(err)
	}
	defer device.UnmapMemory(bmem)
	return append([]byte(nil), px...)
}

var (
	red  = [4]byte{255, 0, 0, 255}
	blue = [4]byte{0, 0, 255, 255} // the clear colour
)

// A degenerate triangle (all three vertices at one corner) draws nothing:
// every pixel keeps the clear colour.
func TestRenderingWithPipeline(t *testing.T) {
	px := draw(t, 4, 4, 0)
	for i := 0; i < 16; i++ {
		if got := [4]byte(px[i*4 : i*4+4]); got != blue {
			t.Fatalf("pixel %d: %v, want the clear colour %v", i, got, blue)
		}
	}
}

// The full-screen triangle (positions from gl_VertexIndex) covers every
// pixel with the fragment shader's colour.
func TestFullScreenTriangle(t *testing.T) {
	px := draw(t, 4, 4, 2)
	for i := 0; i < 16; i++ {
		if got := [4]byte(px[i*4 : i*4+4]); got != red {
			t.Fatalf("pixel %d: %v, want the triangle's %v", i, got, red)
		}
	}
}

// The half triangle — (-1,-1) (1,-1) (-1,1), y down in Vulkan — covers the
// pixels above the anti-diagonal and leaves those below it; the pixels whose
// centres lie on the edge go by the rasterizer's tie rule and are not
// checked.
func TestHalfTriangle(t *testing.T) {
	const n = 8
	px := draw(t, n, n, 1)
	var rows []string
	for y := 0; y < n; y++ {
		row := ""
		for x := 0; x < n; x++ {
			got := [4]byte(px[(y*n+x)*4:][:4])
			switch {
			case got == red:
				row += "#"
			case got == blue:
				row += "."
			default:
				row += "?"
			}
			want := blue
			if x+y < n-1 {
				want = red
			} else if x+y == n-1 {
				continue
			}
			if got != want {
				t.Errorf("pixel (%d,%d): %v, want %v", x, y, got, want)
			}
		}
		rows = append(rows, row)
	}
	t.Logf("drawn:\n%s", strings.Join(rows, "\n"))
}
