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
	"encoding/binary"
	"errors"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/hajimehoshi/ebiten/v2/internal/builtinshader"
	"github.com/hajimehoshi/ebiten/v2/internal/graphics"
	"github.com/hajimehoshi/ebiten/v2/internal/graphicsdriver/vulkan/vk"
	"github.com/hajimehoshi/ebiten/v2/internal/shaderir/spirv"
)

// expected are messages a test provokes on purpose (by substring): logged,
// not failures.
var expected sync.Map // *testing.T → []string

// expectValidation marks messages containing s as expected in t.
func expectValidation(t *testing.T, s string) {
	v, _ := expected.LoadOrStore(t, []string{})
	expected.Store(t, append(v.([]string), s))
	t.Cleanup(func() { expected.Delete(t) })
}

// testInstance is a Vulkan 1.3 instance and its first device with a
// graphics queue, or a skip where there is none.
func testInstance(t *testing.T) (vk.Instance, vk.PhysicalDevice, uint32) {
	t.Helper()
	if v, err := vk.EnumerateInstanceVersion(); err != nil || v < vk.APIVersion13 {
		t.Skipf("no Vulkan 1.3 loader (%v)", err)
	}
	// The validation layer, where installed: every error it reports fails
	// the test (warnings are logged).
	validating := true
	instance, err := vk.CreateInstance("ebitengine-test", vk.APIVersion13, []string{vk.ExtDebugUtils}, []string{vk.LayerKhronosValidation})
	if errors.Is(err, vk.ErrorLayerNotPresent) || errors.Is(err, vk.ErrorExtensionNotPresent) {
		validating = false
		instance, err = vk.CreateInstance("ebitengine-test", vk.APIVersion13, nil, nil)
	}
	if errors.Is(err, vk.ErrorIncompatibleDriver) {
		t.Skip("no Vulkan 1.3 driver installed")
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { vk.DestroyInstance(instance) })
	if validating {
		m, err := vk.CreateDebugUtilsMessenger(instance,
			vk.DebugUtilsMessageSeverityWarningBitEXT|vk.DebugUtilsMessageSeverityErrorBitEXT,
			vk.DebugUtilsMessageTypeGeneralBitEXT|vk.DebugUtilsMessageTypeValidationBitEXT|vk.DebugUtilsMessageTypePerformanceBitEXT,
			func(m vk.DebugMessage) {
				if v, ok := expected.Load(t); ok {
					for _, s := range v.([]string) {
						if strings.Contains(m.Text, s) {
							t.Logf("validation (expected): %s", m.Text)
							return
						}
					}
				}
				if m.Severity&vk.DebugUtilsMessageSeverityErrorBitEXT != 0 {
					t.Errorf("validation: %s: %s", m.ID, m.Text)
				} else {
					t.Logf("validation warning: %s: %s", m.ID, m.Text)
				}
			})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { vk.DestroyDebugUtilsMessenger(instance, m) }) // before the instance (cleanups run last-first)
	} else {
		t.Log("no validation layer installed: tests run without it")
	}
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
	expectValidation(t, "VK_EBITENGINE_no_such_extension")
	expectValidation(t, "Failed to validate extensions")
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
	return drawScene(t, w, h, func(device *vk.Device) scene {
		vs, fs := shaders(t, device, [4]float32{1, 0, 0, 1}, scale)
		return scene{vs: vs, fs: fs, count: 3}
	})
}

// scene is what drawScene draws: shaders, their vertex input (nil: none) and
// vertices (floats, uploaded to a vertex buffer), and how many vertices.
type scene struct {
	vs, fs   vk.ShaderModule
	layout   *vk.VertexLayout
	vertices []float32
	indices  []uint32 // none: a plain draw of count vertices
	// uniforms: a uniform buffer bound as a dynamic uniform buffer (set 0,
	// binding 0, the vertex stage), uniformRange bytes of it seen at
	// uniformOffset (the dynamic offset)
	uniforms      []float32
	uniformRange  uint64
	uniformOffset uint32
	// texture: an RGBA image (texW × texH, rows top-down) uploaded and bound
	// as a combined image sampler (set 0, binding 1, the fragment stage)
	// with Ebitengine's texel sampler
	texture    []byte
	texW, texH uint32
	count      uint32
}

// drawScene renders a scene into a w×h RGBA image cleared blue and reads the
// image back.
func drawScene(t *testing.T, w, h uint32, make func(*vk.Device) scene) []byte {
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

	sc := make(device)
	defer device.DestroyShaderModule(sc.vs)
	defer device.DestroyShaderModule(sc.fs)
	var vbuf vk.Buffer
	if len(sc.vertices) > 0 {
		size := uint64(len(sc.vertices) * 4)
		var err error
		if vbuf, err = device.CreateBuffer(size, vk.BufferUsageVertexBufferBit); err != nil {
			t.Fatal(err)
		}
		defer device.DestroyBuffer(vbuf)
		vreq := device.BufferMemoryRequirements(vbuf)
		vtyp, ok := props.FindMemoryType(vreq.MemoryTypeBits, vk.MemoryPropertyHostVisibleBit|vk.MemoryPropertyHostCoherentBit)
		if !ok {
			t.Skip("no host-visible, coherent memory")
		}
		vmem, err := device.AllocateMemory(vreq.Size, vtyp)
		if err != nil {
			t.Fatal(err)
		}
		defer device.FreeMemory(vmem)
		if err := device.BindBufferMemory(vbuf, vmem, 0); err != nil {
			t.Fatal(err)
		}
		m, err := device.MapMemory(vmem, 0, size)
		if err != nil {
			t.Fatal(err)
		}
		for i, f := range sc.vertices {
			binary.LittleEndian.PutUint32(m[i*4:], math.Float32bits(f))
		}
		device.UnmapMemory(vmem)
	}
	var ibuf vk.Buffer
	if len(sc.indices) > 0 {
		size := uint64(len(sc.indices) * 4)
		var err error
		if ibuf, err = device.CreateBuffer(size, vk.BufferUsageIndexBufferBit); err != nil {
			t.Fatal(err)
		}
		defer device.DestroyBuffer(ibuf)
		ireq := device.BufferMemoryRequirements(ibuf)
		ityp, ok := props.FindMemoryType(ireq.MemoryTypeBits, vk.MemoryPropertyHostVisibleBit|vk.MemoryPropertyHostCoherentBit)
		if !ok {
			t.Skip("no host-visible, coherent memory")
		}
		imem, err := device.AllocateMemory(ireq.Size, ityp)
		if err != nil {
			t.Fatal(err)
		}
		defer device.FreeMemory(imem)
		if err := device.BindBufferMemory(ibuf, imem, 0); err != nil {
			t.Fatal(err)
		}
		m, err := device.MapMemory(imem, 0, size)
		if err != nil {
			t.Fatal(err)
		}
		for i, x := range sc.indices {
			binary.LittleEndian.PutUint32(m[i*4:], x)
		}
		device.UnmapMemory(imem)
	}
	var setLayouts []vk.DescriptorSetLayout
	var set vk.DescriptorSet
	var bindings []vk.DescriptorSetLayoutBinding
	var sizes []vk.DescriptorPoolSize
	if len(sc.uniforms) > 0 {
		bindings = append(bindings, vk.DescriptorSetLayoutBinding{Binding: 0, DescriptorType: vk.DescriptorTypeUniformBufferDynamic, DescriptorCount: 1,
			StageFlags: vk.ShaderStageVertexBit | vk.ShaderStageFragmentBit})
		sizes = append(sizes, vk.DescriptorPoolSize{Type: vk.DescriptorTypeUniformBufferDynamic, DescriptorCount: 1})
	}
	if sc.texture != nil {
		bindings = append(bindings, vk.DescriptorSetLayoutBinding{Binding: 1, DescriptorType: vk.DescriptorTypeCombinedImageSampler, DescriptorCount: 1,
			StageFlags: vk.ShaderStageFragmentBit})
		sizes = append(sizes, vk.DescriptorPoolSize{Type: vk.DescriptorTypeCombinedImageSampler, DescriptorCount: 1})
	}
	if len(bindings) > 0 {
		sl, err := device.CreateDescriptorSetLayout(bindings...)
		if err != nil {
			t.Fatal(err)
		}
		defer device.DestroyDescriptorSetLayout(sl)
		setLayouts = append(setLayouts, sl)
		pool, err := device.CreateDescriptorPool(1, sizes...)
		if err != nil {
			t.Fatal(err)
		}
		defer device.DestroyDescriptorPool(pool)
		if set, err = device.AllocateDescriptorSet(pool, sl); err != nil {
			t.Fatal(err)
		}
	}
	var texImg vk.Image
	var texStage vk.Buffer
	if sc.texture != nil {
		var err error
		if texImg, err = device.CreateImage2D(sc.texW, sc.texH, vk.FormatR8G8B8A8Unorm, vk.ImageUsageSampledBit|vk.ImageUsageTransferDstBit); err != nil {
			t.Fatal(err)
		}
		defer device.DestroyImage(texImg)
		treq := device.ImageMemoryRequirements(texImg)
		ttyp, _ := props.FindMemoryType(treq.MemoryTypeBits, 0)
		tmem, err := device.AllocateMemory(treq.Size, ttyp)
		if err != nil {
			t.Fatal(err)
		}
		defer device.FreeMemory(tmem)
		if err := device.BindImageMemory(texImg, tmem, 0); err != nil {
			t.Fatal(err)
		}
		tview, err := device.CreateImageView2D(texImg, vk.FormatR8G8B8A8Unorm)
		if err != nil {
			t.Fatal(err)
		}
		defer device.DestroyImageView(tview)
		sampler, err := device.CreateTexelSampler()
		if err != nil {
			t.Fatal(err)
		}
		defer device.DestroySampler(sampler)
		if texStage, err = device.CreateBuffer(uint64(len(sc.texture)), vk.BufferUsageTransferSrcBit); err != nil {
			t.Fatal(err)
		}
		defer device.DestroyBuffer(texStage)
		sreq := device.BufferMemoryRequirements(texStage)
		styp, _ := props.FindMemoryType(sreq.MemoryTypeBits, vk.MemoryPropertyHostVisibleBit|vk.MemoryPropertyHostCoherentBit)
		smem, err := device.AllocateMemory(sreq.Size, styp)
		if err != nil {
			t.Fatal(err)
		}
		defer device.FreeMemory(smem)
		if err := device.BindBufferMemory(texStage, smem, 0); err != nil {
			t.Fatal(err)
		}
		mp, err := device.MapMemory(smem, 0, uint64(len(sc.texture)))
		if err != nil {
			t.Fatal(err)
		}
		copy(mp, sc.texture)
		device.UnmapMemory(smem)
		device.WriteCombinedImageSampler(set, 1, sampler, tview, vk.ImageLayoutShaderReadOnlyOptimal)
	}
	if len(sc.uniforms) > 0 {
		size := uint64(len(sc.uniforms) * 4)
		ubuf, err := device.CreateBuffer(size, vk.BufferUsageUniformBufferBit)
		if err != nil {
			t.Fatal(err)
		}
		defer device.DestroyBuffer(ubuf)
		ureq := device.BufferMemoryRequirements(ubuf)
		utyp, ok := props.FindMemoryType(ureq.MemoryTypeBits, vk.MemoryPropertyHostVisibleBit|vk.MemoryPropertyHostCoherentBit)
		if !ok {
			t.Skip("no host-visible, coherent memory")
		}
		umem, err := device.AllocateMemory(ureq.Size, utyp)
		if err != nil {
			t.Fatal(err)
		}
		defer device.FreeMemory(umem)
		if err := device.BindBufferMemory(ubuf, umem, 0); err != nil {
			t.Fatal(err)
		}
		m, err := device.MapMemory(umem, 0, size)
		if err != nil {
			t.Fatal(err)
		}
		for i, f := range sc.uniforms {
			binary.LittleEndian.PutUint32(m[i*4:], math.Float32bits(f))
		}
		device.UnmapMemory(umem)
		device.WriteUniformBuffer(set, 0, ubuf, 0, sc.uniformRange)
	}
	layout, err := device.CreatePipelineLayout(setLayouts...)
	if err != nil {
		t.Fatal(err)
	}
	defer device.DestroyPipelineLayout(layout)
	pipeline, err := device.CreateGraphicsPipeline(layout, sc.vs, sc.fs, sc.layout, vk.FormatR8G8B8A8Unorm)
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
	if texImg != 0 { // the texture uploaded: undefined → transfer dst, the copy, → shader read-only
		device.CmdImageBarrier(cb, vk.ImageMemoryBarrier2{
			DstStageMask: vk.PipelineStage2AllTransfer, DstAccessMask: vk.Access2TransferWrite,
			OldLayout: vk.ImageLayoutUndefined, NewLayout: vk.ImageLayoutTransferDstOptimal,
			SrcQueueFamilyIndex: vk.QueueFamilyIgnored, DstQueueFamilyIndex: vk.QueueFamilyIgnored,
			Image: texImg, SubresourceRange: vk.ColorRange,
		})
		device.CmdCopyBufferToImage(cb, texStage, texImg, vk.ImageLayoutTransferDstOptimal, vk.BufferImageCopy{
			ImageSubresource: vk.ImageSubresourceLayers{AspectMask: vk.ImageAspectColorBit, LayerCount: 1},
			ImageExtent:      vk.Extent3D{Width: sc.texW, Height: sc.texH, Depth: 1},
		})
		device.CmdImageBarrier(cb, vk.ImageMemoryBarrier2{
			SrcStageMask: vk.PipelineStage2AllTransfer, SrcAccessMask: vk.Access2TransferWrite,
			DstStageMask: vk.PipelineStage2FragmentShader, DstAccessMask: vk.Access2ShaderSampledRead,
			OldLayout: vk.ImageLayoutTransferDstOptimal, NewLayout: vk.ImageLayoutShaderReadOnlyOptimal,
			SrcQueueFamilyIndex: vk.QueueFamilyIgnored, DstQueueFamilyIndex: vk.QueueFamilyIgnored,
			Image: texImg, SubresourceRange: vk.ColorRange,
		})
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
	if set != 0 {
		if len(sc.uniforms) > 0 {
			device.CmdBindDescriptorSet(cb, layout, set, sc.uniformOffset)
		} else {
			device.CmdBindDescriptorSet(cb, layout, set)
		}
	}
	device.CmdSetViewport(cb, vk.Viewport{Width: float32(w), Height: float32(h), MaxDepth: 1})
	device.CmdSetScissor(cb, area)
	if vbuf != 0 {
		device.CmdBindVertexBuffer(cb, vbuf, 0)
	}
	if ibuf != 0 {
		device.CmdBindIndexBuffer(cb, ibuf, 0, vk.IndexTypeUint32)
		device.CmdDrawIndexed(cb, uint32(len(sc.indices)), 1, 0, 0, 0)
	} else {
		device.CmdDraw(cb, sc.count, 1, 0, 0)
	}
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

// ebitengineVertex is Ebitengine's vertex as the driver gets it: 12 floats —
// the destination position (2), the source texture position (2), the colour
// (4, premultiplied) and 4 custom values — at Locations 0–3.
var ebitengineVertex = &vk.VertexLayout{Stride: 12 * 4, Attributes: []vk.VertexInputAttributeDescription{
	{Location: 0, Format: vk.FormatR32G32Sfloat, Offset: 0},
	{Location: 1, Format: vk.FormatR32G32Sfloat, Offset: 2 * 4},
	{Location: 2, Format: vk.FormatR32G32B32A32Sfloat, Offset: 4 * 4},
	{Location: 3, Format: vk.FormatR32G32B32A32Sfloat, Offset: 8 * 4},
}}

// colourShaders: a vertex shader taking Ebitengine's vertex (position in clip
// space here — the projection comes with the driver) and handing its colour
// on as a varying at Location 0; a fragment shader writing that colour.
func colourShaders(t *testing.T, device *vk.Device) (vk.ShaderModule, vk.ShaderModule) {
	t.Helper()
	vert := spirv.NewModule()
	vert.Capability(spirv.CapabilityShader)
	vert.MemoryModel(spirv.AddressingLogical, spirv.MemoryModelGLSL450)
	f32 := vert.TypeFloat(32)
	vec2, vec4 := vert.TypeVector(f32, 2), vert.TypeVector(f32, 4)
	in := func(typ, loc uint32) uint32 {
		v := vert.Variable(vert.TypePointer(spirv.StorageClassInput, typ), spirv.StorageClassInput)
		vert.Decorate(v, spirv.DecorationLocation, loc)
		return v
	}
	dst, src, col, custom := in(vec2, 0), in(vec2, 1), in(vec4, 2), in(vec4, 3)
	pos := vert.Variable(vert.TypePointer(spirv.StorageClassOutput, vec4), spirv.StorageClassOutput)
	vert.Decorate(pos, spirv.DecorationBuiltIn, spirv.BuiltInPosition)
	vcol := vert.Variable(vert.TypePointer(spirv.StorageClassOutput, vec4), spirv.StorageClassOutput)
	vert.Decorate(vcol, spirv.DecorationLocation, 0)
	zero, one := vert.ConstantFloat32(0), vert.ConstantFloat32(1)
	void := vert.TypeVoid()
	vmain := vert.Function(void, vert.TypeFunction(void), func(f *spirv.Function) {
		f.Label()
		d := f.Load(vec2, dst)
		f.Store(pos, f.CompositeConstruct(vec4, f.CompositeExtract(f32, d, 0), f.CompositeExtract(f32, d, 1), zero, one))
		f.Store(vcol, f.Load(vec4, col))
		f.Return()
	})
	vert.EntryPoint(spirv.ExecutionModelVertex, vmain, "main", dst, src, col, custom, pos, vcol)

	frag := spirv.NewModule()
	frag.Capability(spirv.CapabilityShader)
	frag.MemoryModel(spirv.AddressingLogical, spirv.MemoryModelGLSL450)
	fvec4 := frag.TypeVector(frag.TypeFloat(32), 4)
	fin := frag.Variable(frag.TypePointer(spirv.StorageClassInput, fvec4), spirv.StorageClassInput)
	frag.Decorate(fin, spirv.DecorationLocation, 0)
	out := frag.Variable(frag.TypePointer(spirv.StorageClassOutput, fvec4), spirv.StorageClassOutput)
	frag.Decorate(out, spirv.DecorationLocation, 0)
	fvoid := frag.TypeVoid()
	fmain := frag.Function(fvoid, frag.TypeFunction(fvoid), func(f *spirv.Function) {
		f.Label()
		f.Store(out, f.Load(fvec4, fin))
		f.Return()
	})
	frag.EntryPoint(spirv.ExecutionModelFragment, fmain, "main", fin, out)
	frag.ExecutionMode(fmain, spirv.ExecutionModeOriginUpperLeft)

	for _, m := range []*spirv.Module{vert, frag} {
		spirvVal(t, m.Bytes())
	}
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

// Vertex input: one triangle from a vertex buffer in Ebitengine's layout,
// red / green / blue at its corners, covering the top-left half of a 16×16
// image; the colours interpolate across it. Checked: each corner pixel is
// nearly its vertex's colour, a pixel in the middle mixes them, and the
// other half keeps the clear colour.
func TestVertexInputInterpolates(t *testing.T) {
	const n = 16
	px := drawScene(t, n, n, func(device *vk.Device) scene {
		vs, fs := colourShaders(t, device)
		v := func(x, y, r, g, b float32) []float32 { return []float32{x, y, 0, 0, r, g, b, 1, 0, 0, 0, 0} }
		verts := slices.Concat(v(-1, -1, 1, 0, 0), v(1, -1, 0, 1, 0), v(-1, 1, 0, 0, 1))
		return scene{vs: vs, fs: fs, layout: ebitengineVertex, vertices: verts, count: 3}
	})
	at := func(x, y int) [4]byte { i := (y*n + x) * 4; return [4]byte(px[i : i+4]) }
	near := func(got, want [4]byte) bool {
		for i := range got {
			if d := int(got[i]) - int(want[i]); d < -40 || d > 40 {
				return false
			}
		}
		return true
	}
	if c := at(0, 0); !near(c, [4]byte{255, 0, 0, 255}) {
		t.Errorf("top-left %v: want nearly red", c)
	}
	if c := at(n-2, 0); !near(c, [4]byte{0, 255, 0, 255}) {
		t.Errorf("top-right %v: want nearly green", c)
	}
	if c := at(0, n-2); !near(c, [4]byte{0, 0, 255, 255}) {
		t.Errorf("bottom-left %v: want nearly blue", c)
	}
	if c := at(4, 4); c[0] < 60 || c[1] < 40 || c[2] < 40 {
		t.Errorf("inside %v: want a mix of all three", c)
	}
	if c := at(n-1, n-1); c != blue {
		t.Errorf("bottom-right %v: want the clear colour", c)
	}
}

// spirvVal checks a module with spirv-val (Vulkan 1.3 rules) where
// SPIRV-Tools is installed.
func spirvVal(t *testing.T, b []byte) {
	t.Helper()
	val, err := exec.LookPath("spirv-val")
	if err != nil {
		return
	}
	p := filepath.Join(t.TempDir(), "m.spv")
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(val, "--target-env", "vulkan1.3", p).CombinedOutput(); err != nil {
		t.Fatalf("spirv-val: %v\n%s", err, out)
	}
}

// Indexed drawing: a quad over the whole image from 4 vertices and 6 uint32
// indices (two triangles sharing an edge, as Ebitengine sends quads), all
// corners green — every pixel green, none left the clear colour (a gap on
// the shared diagonal would show).
func TestIndexedQuad(t *testing.T) {
	const n = 8
	px := drawScene(t, n, n, func(device *vk.Device) scene {
		vs, fs := colourShaders(t, device)
		v := func(x, y float32) []float32 { return []float32{x, y, 0, 0, 0, 1, 0, 1, 0, 0, 0, 0} }
		verts := slices.Concat(v(-1, -1), v(1, -1), v(-1, 1), v(1, 1))
		return scene{vs: vs, fs: fs, layout: ebitengineVertex, vertices: verts, indices: []uint32{0, 1, 2, 1, 2, 3}}
	})
	green := [4]byte{0, 255, 0, 255}
	for i := 0; i < n*n; i++ {
		if got := [4]byte(px[i*4 : i*4+4]); got != green {
			t.Fatalf("pixel %d,%d: %v, want green", i%n, i/n, got)
		}
	}
}

// projectionShaders: colourShaders' vertex shader with the position through
// a uniform block — gl_Position = projection * vec4(dst, 0, 1), the block
// (set 0, binding 0) holding one column-major mat4 at offset 0, std140 — as
// the driver will send Ebitengine's pixel-to-clip projection; the fragment
// shader writes the colour.
func projectionShaders(t *testing.T, device *vk.Device) (vk.ShaderModule, vk.ShaderModule) {
	t.Helper()
	vert := spirv.NewModule()
	vert.Capability(spirv.CapabilityShader)
	vert.MemoryModel(spirv.AddressingLogical, spirv.MemoryModelGLSL450)
	f32 := vert.TypeFloat(32)
	vec2, vec4 := vert.TypeVector(f32, 2), vert.TypeVector(f32, 4)
	mat4 := vert.TypeMatrix(vec4, 4)
	block := vert.TypeStruct(mat4)
	vert.Decorate(block, spirv.DecorationBlock)
	vert.MemberDecorate(block, 0, spirv.DecorationOffset, 0)
	vert.MemberDecorate(block, 0, spirv.DecorationColMajor)
	vert.MemberDecorate(block, 0, spirv.DecorationMatrixStride, 16)
	uniforms := vert.Variable(vert.TypePointer(spirv.StorageClassUniform, block), spirv.StorageClassUniform)
	vert.Decorate(uniforms, spirv.DecorationDescriptorSet, 0)
	vert.Decorate(uniforms, spirv.DecorationBinding, 0)
	in := func(typ, loc uint32) uint32 {
		v := vert.Variable(vert.TypePointer(spirv.StorageClassInput, typ), spirv.StorageClassInput)
		vert.Decorate(v, spirv.DecorationLocation, loc)
		return v
	}
	dst, src, col, custom := in(vec2, 0), in(vec2, 1), in(vec4, 2), in(vec4, 3)
	pos := vert.Variable(vert.TypePointer(spirv.StorageClassOutput, vec4), spirv.StorageClassOutput)
	vert.Decorate(pos, spirv.DecorationBuiltIn, spirv.BuiltInPosition)
	vcol := vert.Variable(vert.TypePointer(spirv.StorageClassOutput, vec4), spirv.StorageClassOutput)
	vert.Decorate(vcol, spirv.DecorationLocation, 0)
	zero, one, member0 := vert.ConstantFloat32(0), vert.ConstantFloat32(1), vert.ConstantInt32(0)
	matPtr := vert.TypePointer(spirv.StorageClassUniform, mat4)
	void := vert.TypeVoid()
	vmain := vert.Function(void, vert.TypeFunction(void), func(f *spirv.Function) {
		f.Label()
		d := f.Load(vec2, dst)
		p := f.CompositeConstruct(vec4, f.CompositeExtract(f32, d, 0), f.CompositeExtract(f32, d, 1), zero, one)
		proj := f.Load(mat4, f.AccessChain(matPtr, uniforms, member0))
		f.Store(pos, f.MatrixTimesVector(vec4, proj, p))
		f.Store(vcol, f.Load(vec4, col))
		f.Return()
	})
	vert.EntryPoint(spirv.ExecutionModelVertex, vmain, "main", dst, src, col, custom, pos, vcol, uniforms) // SPIR-V 1.4+: every global it uses
	spirvVal(t, vert.Bytes())
	unused, fs := colourShaders(t, device) // the same fragment shader
	device.DestroyShaderModule(unused)
	vs, err := device.CreateShaderModule(vert.Words())
	if err != nil {
		t.Fatal(err)
	}
	return vs, fs
}

// Uniforms: a quad given in pixels (0..8 × 0..16 of a 16×16 image), placed
// by a projection matrix from a dynamic uniform buffer holding two matrices
// 256 bytes apart (the most minUniformBufferOffsetAlignment may be). The
// dynamic offset picks one: at 0 (pixels → clip) the left half is green; at
// 256 (the same, 8 pixels to the right) the right half is.
func TestUniformProjection(t *testing.T) {
	const n = 16
	ortho := func(dx float32) []float32 { // column-major: x/8 − 1 + dx/8, y/8 − 1
		return []float32{2.0 / n, 0, 0, 0, 0, 2.0 / n, 0, 0, 0, 0, 1, 0, -1 + dx*2/n, -1, 0, 1}
	}
	uniforms := make([]float32, 128) // 512 bytes: a matrix at 0 and at 256
	copy(uniforms, ortho(0))
	copy(uniforms[64:], ortho(8))
	green := [4]byte{0, 255, 0, 255}
	for _, c := range []struct {
		offset    uint32
		greenLeft bool
	}{{0, true}, {256, false}} {
		px := drawScene(t, n, n, func(device *vk.Device) scene {
			vs, fs := projectionShaders(t, device)
			v := func(x, y float32) []float32 { return []float32{x, y, 0, 0, 0, 1, 0, 1, 0, 0, 0, 0} }
			verts := slices.Concat(v(0, 0), v(8, 0), v(0, 16), v(8, 16))
			return scene{vs: vs, fs: fs, layout: ebitengineVertex, vertices: verts, indices: []uint32{0, 1, 2, 1, 2, 3},
				uniforms: uniforms, uniformRange: 64, uniformOffset: c.offset}
		})
		for y := 0; y < n; y++ {
			for x := 0; x < n; x++ {
				i := (y*n + x) * 4
				got := [4]byte(px[i : i+4])
				if want := (x < n/2) == c.greenLeft; (got == green) != want {
					t.Fatalf("offset %d: pixel %d,%d %v (green expected: %v)", c.offset, x, y, got, want)
				}
			}
		}
	}
}

// The SPIR-V backend's first draw: Ebitengine's built-in vertex function,
// compiled by spirv.CompileVertex from the IR of a Kage program, with a
// constant-green fragment; the uniforms are Ebitengine's own []uint32 (the
// seven preserved ones, the projection a pixels-to-clip ortho at dword 30,
// y = 0 → NDC −1) packed by spirv.PackStd140. A quad over the top half in
// pixels (y 0…8) must land in memory rows 0…7 — so Vulkan takes Ebitengine's
// projection as it is: its NDC y points down like its framebuffer (OpenGL
// offscreen gives the same rows; Metal and DirectX flip because their NDC
// points up). With Metal's flip the quad lands upside down — checked too.
func TestCompiledVertexShader(t *testing.T) {
	const n = 16
	p, err := graphics.CompileShader([]byte("//kage:unit pixels\npackage main\nfunc Fragment(dst vec4, src vec2, color vec4) vec4 { return vec4(1, 0, 0, 1) }\n"))
	if err != nil {
		t.Fatal(err)
	}
	vsWords, err := spirv.CompileVertex(p)
	if err != nil {
		t.Fatal(err)
	}
	src := make([]uint32, 46) // the preserved uniforms' dwords: 2 + 8 + 2 + 2 + 8 + 8 + 16
	ortho := []float32{2.0 / n, 0, 0, 0, 0, 2.0 / n, 0, 0, 0, 0, 1, 0, -1, -1, 0, 1}
	for i, f := range ortho {
		src[30+i] = math.Float32bits(f)
	}
	green := [4]byte{0, 255, 0, 255}
	for _, flip := range []bool{false, true} {
		flipY := -1
		if flip {
			flipY = graphics.ProjectionMatrixUniformVariableIndex
		}
		packed, err := spirv.PackStd140(nil, p.Uniforms, src, flipY)
		if err != nil {
			t.Fatal(err)
		}
		uniforms := make([]float32, len(packed))
		for i, w := range packed {
			uniforms[i] = math.Float32frombits(w)
		}
		px := drawScene(t, n, n, func(device *vk.Device) scene {
			vs, err := device.CreateShaderModule(vsWords)
			if err != nil {
				t.Fatal(err)
			}
			v := func(x, y float32) []float32 { return []float32{x, y, 0, 0, 1, 1, 1, 1, 0, 0, 0, 0} }
			verts := slices.Concat(v(0, 0), v(16, 0), v(0, 8), v(16, 8))
			return scene{vs: vs, fs: greenFragment(t, device), layout: ebitengineVertex, vertices: verts, indices: []uint32{0, 1, 2, 1, 2, 3},
				uniforms: uniforms, uniformRange: uint64(len(packed) * 4)}
		})
		for y := 0; y < n; y++ {
			for x := 0; x < n; x++ {
				i := (y*n + x) * 4
				want := y < n/2 != flip
				if got := [4]byte(px[i : i+4]); (got == green) != want {
					t.Fatalf("flip %v: pixel %d,%d: %v (green expected: %v)", flip, x, y, got, want)
				}
			}
		}
	}
}

// greenFragment is a fragment shader writing constant green.
func greenFragment(t *testing.T, device *vk.Device) vk.ShaderModule {
	t.Helper()
	frag := spirv.NewModule()
	frag.Capability(spirv.CapabilityShader)
	frag.MemoryModel(spirv.AddressingLogical, spirv.MemoryModelGLSL450)
	vec4 := frag.TypeVector(frag.TypeFloat(32), 4)
	out := frag.Variable(frag.TypePointer(spirv.StorageClassOutput, vec4), spirv.StorageClassOutput)
	frag.Decorate(out, spirv.DecorationLocation, 0)
	green := frag.ConstantComposite(vec4, frag.ConstantFloat32(0), frag.ConstantFloat32(1), frag.ConstantFloat32(0), frag.ConstantFloat32(1))
	void := frag.TypeVoid()
	fmain := frag.Function(void, frag.TypeFunction(void), func(f *spirv.Function) {
		f.Label()
		f.Store(out, green)
		f.Return()
	})
	frag.EntryPoint(spirv.ExecutionModelFragment, fmain, "main", out)
	frag.ExecutionMode(fmain, spirv.ExecutionModeOriginUpperLeft)
	fs, err := device.CreateShaderModule(frag.Words())
	if err != nil {
		t.Fatal(err)
	}
	return fs
}

// Kage end to end: both stages compiled from a Kage program by the SPIR-V
// backend and drawn on lavapipe — the red program (return vec4(1, 0, 0, 1))
// paints its quad red; "return color" paints the vertices' colour (the
// varying from the vertex stage, at its Location), green here.
func TestKageEndToEnd(t *testing.T) {
	const n = 16
	src := make([]uint32, 46)
	for i, f := range []float32{2.0 / n, 0, 0, 0, 0, 2.0 / n, 0, 0, 0, 0, 1, 0, -1, -1, 0, 1} {
		src[30+i] = math.Float32bits(f)
	}
	for _, c := range []struct {
		body   string
		colour [4]float32
		want   [4]byte
	}{
		{"return vec4(1, 0, 0, 1)", [4]float32{0, 0, 1, 1}, [4]byte{255, 0, 0, 255}},
		{"return color", [4]float32{0, 1, 0, 1}, [4]byte{0, 255, 0, 255}},
		// a local, *= by a scalar, a user function, + a vector: (0,1,0,1) → (0,2,0,2) → (0,1,0,1) → (0,1,0.25,1)
		{"clr := color\n\tclr *= 2.0\n\treturn half(clr) + vec4(0, 0, 0.25, 0)", [4]float32{0, 1, 0, 1}, [4]byte{0, 255, 64, 255}},
	} {
		p, err := graphics.CompileShader([]byte("//kage:unit pixels\npackage main\nfunc half(c vec4) vec4 { return c * 0.5 }\nfunc Fragment(dst vec4, src vec2, color vec4) vec4 {\n\t" + c.body + "\n}\n"))
		if err != nil {
			t.Fatal(err)
		}
		vsWords, err := spirv.CompileVertex(p)
		if err != nil {
			t.Fatal(err)
		}
		fsWords, err := spirv.CompileFragment(p)
		if err != nil {
			t.Fatalf("%s: %v", c.body, err)
		}
		packed, err := spirv.PackStd140(nil, p.Uniforms, src, -1)
		if err != nil {
			t.Fatal(err)
		}
		uniforms := make([]float32, len(packed))
		for i, w := range packed {
			uniforms[i] = math.Float32frombits(w)
		}
		px := drawScene(t, n, n, func(device *vk.Device) scene {
			vs, err := device.CreateShaderModule(vsWords)
			if err != nil {
				t.Fatal(err)
			}
			fs, err := device.CreateShaderModule(fsWords)
			if err != nil {
				t.Fatal(err)
			}
			col := c.colour
			v := func(x, y float32) []float32 { return []float32{x, y, 0, 0, col[0], col[1], col[2], col[3], 0, 0, 0, 0} }
			verts := slices.Concat(v(0, 0), v(16, 0), v(0, 8), v(16, 8))
			return scene{vs: vs, fs: fs, layout: ebitengineVertex, vertices: verts, indices: []uint32{0, 1, 2, 1, 2, 3},
				uniforms: uniforms, uniformRange: uint64(len(packed) * 4)}
		})
		for y := 0; y < n; y++ {
			for x := 0; x < n; x++ {
				i := (y*n + x) * 4
				got := [4]byte(px[i : i+4])
				if want := y < n/2; (got == c.want) != want {
					t.Fatalf("%s: pixel %d,%d: %v (%v expected: %v)", c.body, x, y, got, c.want, want)
				}
			}
		}
	}
}

// Sampling with Ebitengine's texel sampler: a 2×2 texture (red, green /
// blue, white) drawn over a 4×2 target, each pixel sampling at its own
// position (gl_FragCoord.xy: pixel centres, unnormalized, explicit LOD 0):
// the left 2×2 is the texture texel for texel; the right half, outside it,
// transparent (clamp-to-border, transparent black) — as Metal's
// clamp_to_zero.
func TestTexelSampling(t *testing.T) {
	tex := []byte{255, 0, 0, 255, 0, 255, 0, 255, 0, 0, 255, 255, 255, 255, 255, 255}
	px := drawScene(t, 4, 2, func(device *vk.Device) scene {
		vs, unused := shaders(t, device, [4]float32{0, 0, 0, 1}, 2) // the full-target triangle
		device.DestroyShaderModule(unused)
		frag := spirv.NewModule()
		frag.Capability(spirv.CapabilityShader)
		frag.MemoryModel(spirv.AddressingLogical, spirv.MemoryModelGLSL450)
		f32 := frag.TypeFloat(32)
		vec2, vec4 := frag.TypeVector(f32, 2), frag.TypeVector(f32, 4)
		simg := frag.TypeSampledImage(frag.TypeImage2D())
		texVar := frag.Variable(frag.TypePointer(spirv.StorageClassUniformConstant, simg), spirv.StorageClassUniformConstant)
		frag.Decorate(texVar, spirv.DecorationDescriptorSet, 0)
		frag.Decorate(texVar, spirv.DecorationBinding, 1)
		coord := frag.Variable(frag.TypePointer(spirv.StorageClassInput, vec4), spirv.StorageClassInput)
		frag.Decorate(coord, spirv.DecorationBuiltIn, spirv.BuiltInFragCoord)
		out := frag.Variable(frag.TypePointer(spirv.StorageClassOutput, vec4), spirv.StorageClassOutput)
		frag.Decorate(out, spirv.DecorationLocation, 0)
		lod := frag.ConstantFloat32(0)
		void := frag.TypeVoid()
		fmain := frag.Function(void, frag.TypeFunction(void), func(f *spirv.Function) {
			f.Label()
			fc := f.Load(vec4, coord)
			xy := f.VectorShuffle(vec2, fc, fc, 0, 1)
			f.Store(out, f.ImageSampleExplicitLod(vec4, f.Load(simg, texVar), xy, lod))
			f.Return()
		})
		frag.EntryPoint(spirv.ExecutionModelFragment, fmain, "main", coord, out, texVar)
		frag.ExecutionMode(fmain, spirv.ExecutionModeOriginUpperLeft)
		spirvVal(t, frag.Bytes())
		fs, err := device.CreateShaderModule(frag.Words())
		if err != nil {
			t.Fatal(err)
		}
		return scene{vs: vs, fs: fs, count: 3, texture: tex, texW: 2, texH: 2}
	})
	want := [][4]byte{
		{255, 0, 0, 255}, {0, 255, 0, 255}, {0, 0, 0, 0}, {0, 0, 0, 0},
		{0, 0, 255, 255}, {255, 255, 255, 255}, {0, 0, 0, 0}, {0, 0, 0, 0},
	}
	for i, w := range want {
		if got := [4]byte(px[i*4 : i*4+4]); got != w {
			t.Errorf("pixel %d,%d: %v, want %v", i%4, i/4, got, w)
		}
	}
}

// Ebitengine's nearest-filter built-in shader (imageSrc0UnsafeAt × colour)
// from Kage, both stages compiled by the SPIR-V backend, drawing a 2×2
// texture onto a 4×2 target: the source position equals the destination
// (pixels), the colour white — the left half is the texture texel for texel,
// the right half (outside the source) transparent.
func TestKageNearestBuiltin(t *testing.T) {
	p, err := graphics.CompileShader([]byte("//kage:unit pixels\npackage main\nfunc Fragment(dstPos vec4, src0Pos vec2, color vec4) vec4 {\n\tclr := imageSrc0UnsafeAt(src0Pos)\n\tclr *= color\n\treturn clr\n}\n"))
	if err != nil {
		t.Fatal(err)
	}
	vsWords, err := spirv.CompileVertex(p)
	if err != nil {
		t.Fatal(err)
	}
	fsWords, err := spirv.CompileFragment(p)
	if err != nil {
		t.Fatal(err)
	}
	const w, h = 4, 2
	src := make([]uint32, 46)
	for i, f := range []float32{2.0 / w, 0, 0, 0, 0, 2.0 / h, 0, 0, 0, 0, 1, 0, -1, -1, 0, 1} {
		src[30+i] = math.Float32bits(f)
	}
	packed, err := spirv.PackStd140(nil, p.Uniforms, src, -1)
	if err != nil {
		t.Fatal(err)
	}
	uniforms := make([]float32, len(packed))
	for i, x := range packed {
		uniforms[i] = math.Float32frombits(x)
	}
	tex := []byte{255, 0, 0, 255, 0, 255, 0, 255, 0, 0, 255, 255, 255, 255, 255, 255}
	px := drawScene(t, w, h, func(device *vk.Device) scene {
		vs, err := device.CreateShaderModule(vsWords)
		if err != nil {
			t.Fatal(err)
		}
		fs, err := device.CreateShaderModule(fsWords)
		if err != nil {
			t.Fatal(err)
		}
		v := func(x, y float32) []float32 { return []float32{x, y, x, y, 1, 1, 1, 1, 0, 0, 0, 0} } // src = dst
		verts := slices.Concat(v(0, 0), v(w, 0), v(0, h), v(w, h))
		return scene{vs: vs, fs: fs, layout: ebitengineVertex, vertices: verts, indices: []uint32{0, 1, 2, 1, 2, 3},
			uniforms: uniforms, uniformRange: uint64(len(packed) * 4), texture: tex, texW: 2, texH: 2}
	})
	want := [][4]byte{
		{255, 0, 0, 255}, {0, 255, 0, 255}, {0, 0, 0, 0}, {0, 0, 0, 0},
		{0, 0, 255, 255}, {255, 255, 255, 255}, {0, 0, 0, 0}, {0, 0, 0, 0},
	}
	for i, wt := range want {
		if got := [4]byte(px[i*4 : i*4+4]); got != wt {
			t.Errorf("pixel %d,%d: %v, want %v", i%w, i/w, got, wt)
		}
	}
}

// Ebitengine's clamp-to-zero nearest built-in (imageSrc0At: the source
// region checked with step(), the uniforms' region arrays indexed) drawing a
// 2×2 texture whose source region is set to 1×2: only the texture's first
// column shows; its second column — inside the texture, outside the region —
// is transparent by the shader's check, as are the target's right two
// columns.
func TestKageClampToZeroBuiltin(t *testing.T) {
	p, err := graphics.CompileShader(builtinshader.ShaderSource(builtinshader.FilterNearest, builtinshader.AddressClampToZero))
	if err != nil {
		t.Fatal(err)
	}
	vsWords, err := spirv.CompileVertex(p)
	if err != nil {
		t.Fatal(err)
	}
	fsWords, err := spirv.CompileFragment(p)
	if err != nil {
		t.Fatal(err)
	}
	const w, h = 4, 2
	src := make([]uint32, 46)
	set := func(i int, v ...float32) {
		for j, f := range v {
			src[i+j] = math.Float32bits(f)
		}
	}
	set(2, 2, 2)  // __imageSrcTextureSizes[0]
	set(14, 0, 0) // __imageSrcRegionOrigins[0]
	set(22, 1, 2) // __imageSrcRegionSizes[0]: one column
	set(30, 2.0/w, 0, 0, 0, 0, 2.0/h, 0, 0, 0, 0, 1, 0, -1, -1, 0, 1)
	packed, err := spirv.PackStd140(nil, p.Uniforms, src, -1)
	if err != nil {
		t.Fatal(err)
	}
	uniforms := make([]float32, len(packed))
	for i, x := range packed {
		uniforms[i] = math.Float32frombits(x)
	}
	tex := []byte{255, 0, 0, 255, 0, 255, 0, 255, 0, 0, 255, 255, 255, 255, 255, 255}
	px := drawScene(t, w, h, func(device *vk.Device) scene {
		vs, err := device.CreateShaderModule(vsWords)
		if err != nil {
			t.Fatal(err)
		}
		fs, err := device.CreateShaderModule(fsWords)
		if err != nil {
			t.Fatal(err)
		}
		v := func(x, y float32) []float32 { return []float32{x, y, x, y, 1, 1, 1, 1, 0, 0, 0, 0} }
		verts := slices.Concat(v(0, 0), v(w, 0), v(0, h), v(w, h))
		return scene{vs: vs, fs: fs, layout: ebitengineVertex, vertices: verts, indices: []uint32{0, 1, 2, 1, 2, 3},
			uniforms: uniforms, uniformRange: uint64(len(packed) * 4), texture: tex, texW: 2, texH: 2}
	})
	clear := [4]byte{0, 0, 0, 0}
	want := [][4]byte{
		{255, 0, 0, 255}, clear, clear, clear,
		{0, 0, 255, 255}, clear, clear, clear,
	}
	for i, wt := range want {
		if got := [4]byte(px[i*4 : i*4+4]); got != wt {
			t.Errorf("pixel %d,%d: %v, want %v", i%w, i/w, got, wt)
		}
	}
}

// Control flow from Kage: if / else with returns in both branches, && and
// || (short-circuit), < and >=, a local declared in an if block — per pixel
// of a 4×2 target (dst is the pixel centre): row 0 red, red, green, blue;
// row 1 all blue.
func TestKageControlFlow(t *testing.T) {
	const w, h = 4, 2
	p, err := graphics.CompileShader([]byte(`//kage:unit pixels
package main

func Fragment(dst vec4, src vec2, color vec4) vec4 {
	if dst.x < 2 && dst.y < 1 {
		red := vec4(1, 0, 0, 1)
		return red
	}
	if dst.x >= 3 || dst.y >= 1 {
		return vec4(0, 0, 1, 1)
	} else {
		return vec4(0, 1, 0, 1)
	}
}
`))
	if err != nil {
		t.Fatal(err)
	}
	vsWords, err := spirv.CompileVertex(p)
	if err != nil {
		t.Fatal(err)
	}
	fsWords, err := spirv.CompileFragment(p)
	if err != nil {
		t.Fatal(err)
	}
	spirvVal(t, wordsBytes(fsWords))
	src := make([]uint32, 46)
	for i, f := range []float32{2.0 / w, 0, 0, 0, 0, 2.0 / h, 0, 0, 0, 0, 1, 0, -1, -1, 0, 1} {
		src[30+i] = math.Float32bits(f)
	}
	packed, err := spirv.PackStd140(nil, p.Uniforms, src, -1)
	if err != nil {
		t.Fatal(err)
	}
	uniforms := make([]float32, len(packed))
	for i, x := range packed {
		uniforms[i] = math.Float32frombits(x)
	}
	px := drawScene(t, w, h, func(device *vk.Device) scene {
		vs, err := device.CreateShaderModule(vsWords)
		if err != nil {
			t.Fatal(err)
		}
		fs, err := device.CreateShaderModule(fsWords)
		if err != nil {
			t.Fatal(err)
		}
		v := func(x, y float32) []float32 { return []float32{x, y, 0, 0, 1, 1, 1, 1, 0, 0, 0, 0} }
		verts := slices.Concat(v(0, 0), v(w, 0), v(0, h), v(w, h))
		return scene{vs: vs, fs: fs, layout: ebitengineVertex, vertices: verts, indices: []uint32{0, 1, 2, 1, 2, 3},
			uniforms: uniforms, uniformRange: uint64(len(packed) * 4)}
	})
	red, green, blue := [4]byte{255, 0, 0, 255}, [4]byte{0, 255, 0, 255}, [4]byte{0, 0, 255, 255}
	want := [][4]byte{red, red, green, blue, blue, blue, blue, blue}
	for i, wt := range want {
		if got := [4]byte(px[i*4 : i*4+4]); got != wt {
			t.Errorf("pixel %d,%d: %v, want %v", i%w, i/w, got, wt)
		}
	}
}

func wordsBytes(w []uint32) []byte {
	b := make([]byte, 4*len(w))
	for i, x := range w {
		binary.LittleEndian.PutUint32(b[4*i:], x)
	}
	return b
}

// Loops from Kage: a float for loop with continue (at 3) and break (once the
// counter passes the pixel's x), summing 0.1s into red; an int loop counting
// to 4, as float(n) / 4 into green — per pixel of a 4×1 target red is 0.1,
// 0.2, 0.3, 0.3 (pixel centres 0.5 … 3.5), green 1.
func TestKageLoops(t *testing.T) {
	const w, h = 4, 1
	p, err := graphics.CompileShader([]byte(`//kage:unit pixels
package main

func Fragment(dst vec4, src vec2, color vec4) vec4 {
	sum := 0.0
	for i := 0.0; i < 10.0; i += 1.0 {
		if i == 3.0 {
			continue
		}
		if i >= dst.x {
			break
		}
		sum += 0.1
	}
	n := 0
	for j := 0; j < 4; j++ {
		n++
	}
	return vec4(sum, float(n)/4, 0, 1)
}
`))
	if err != nil {
		t.Fatal(err)
	}
	vsWords, err := spirv.CompileVertex(p)
	if err != nil {
		t.Fatal(err)
	}
	fsWords, err := spirv.CompileFragment(p)
	if err != nil {
		t.Fatal(err)
	}
	spirvVal(t, wordsBytes(fsWords))
	src := make([]uint32, 46)
	for i, f := range []float32{2.0 / w, 0, 0, 0, 0, 2.0 / h, 0, 0, 0, 0, 1, 0, -1, -1, 0, 1} {
		src[30+i] = math.Float32bits(f)
	}
	packed, err := spirv.PackStd140(nil, p.Uniforms, src, -1)
	if err != nil {
		t.Fatal(err)
	}
	uniforms := make([]float32, len(packed))
	for i, x := range packed {
		uniforms[i] = math.Float32frombits(x)
	}
	px := drawScene(t, w, h, func(device *vk.Device) scene {
		vs, err := device.CreateShaderModule(vsWords)
		if err != nil {
			t.Fatal(err)
		}
		fs, err := device.CreateShaderModule(fsWords)
		if err != nil {
			t.Fatal(err)
		}
		v := func(x, y float32) []float32 { return []float32{x, y, 0, 0, 1, 1, 1, 1, 0, 0, 0, 0} }
		verts := slices.Concat(v(0, 0), v(w, 0), v(0, h), v(w, h))
		return scene{vs: vs, fs: fs, layout: ebitengineVertex, vertices: verts, indices: []uint32{0, 1, 2, 1, 2, 3},
			uniforms: uniforms, uniformRange: uint64(len(packed) * 4)}
	})
	for i, red := range []float64{0.1, 0.2, 0.3, 0.3} {
		got := px[i*4 : i*4+4]
		if d := int(got[0]) - int(math.Round(red*255)); d < -1 || d > 1 || got[1] != 255 || got[3] != 255 {
			t.Errorf("pixel %d: %v, want red ≈ %d, green 255", i, got, int(math.Round(red*255)))
		}
	}
}
