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
	"testing"

	"github.com/hajimehoshi/ebiten/v2/internal/graphicsdriver/vulkan/vk"
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
