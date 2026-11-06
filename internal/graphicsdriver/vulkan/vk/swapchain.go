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

package vk

import (
	"fmt"
	"runtime"
	"unsafe"

	"github.com/ebitengine/purego"
)

// The window system (V5): VK_KHR_surface on an X11 window (VK_KHR_xlib_surface),
// VK_KHR_swapchain, semaphores and present.

// Surface and swapchain extension names.
const (
	ExtSurface     = "VK_KHR_surface"
	ExtXlibSurface = "VK_KHR_xlib_surface"
	ExtSwapchain   = "VK_KHR_swapchain"
)

type (
	SurfaceKHR   uint64 // VkSurfaceKHR
	SwapchainKHR uint64 // VkSwapchainKHR
	Semaphore    uint64 // VkSemaphore
)

const (
	StructureTypeSemaphoreCreateInfo      StructureType = 9
	StructureTypeSwapchainCreateInfoKHR   StructureType = 1000001000
	StructureTypePresentInfoKHR           StructureType = 1000001001
	StructureTypeXlibSurfaceCreateInfoKHR StructureType = 1000004000
	ImageLayoutPresentSrcKHR              uint32        = 1000001002
	ColorSpaceSRGBNonlinearKHR            uint32        = 0
	PresentModeImmediateKHR               uint32        = 0
	PresentModeMailboxKHR                 uint32        = 1
	PresentModeFifoKHR                    uint32        = 2
	CompositeAlphaOpaqueBitKHR            uint32        = 0x1
	SurfaceTransformIdentityBitKHR        uint32        = 0x1
	SharingModeExclusive                  uint32        = 0
	FormatB8G8R8A8Unorm                   uint32        = 44
	PipelineStageColorAttachmentOutputBit uint32        = 0x400
	SuboptimalKHR                         Result        = 1000001003
	ErrorOutOfDateKHR                     Result        = -1000001004
	ErrorSurfaceLostKHR                   Result        = -1000000000
)

// XlibSurfaceCreateInfoKHR is VkXlibSurfaceCreateInfoKHR.
type XlibSurfaceCreateInfoKHR struct {
	SType  StructureType
	PNext  unsafe.Pointer
	Flags  uint32
	Dpy    uintptr // Display*
	Window uint64  // Window (an XID: unsigned long)
}

// SurfaceCapabilitiesKHR is VkSurfaceCapabilitiesKHR.
type SurfaceCapabilitiesKHR struct {
	MinImageCount, MaxImageCount                  uint32
	CurrentExtent, MinImageExtent, MaxImageExtent Extent2D
	MaxImageArrayLayers, SupportedTransforms      uint32
	CurrentTransform, SupportedCompositeAlpha     uint32
	SupportedUsageFlags                           uint32
}

// SurfaceFormatKHR is VkSurfaceFormatKHR.
type SurfaceFormatKHR struct{ Format, ColorSpace uint32 }

// SwapchainCreateInfoKHR is VkSwapchainCreateInfoKHR.
type SwapchainCreateInfoKHR struct {
	SType                 StructureType
	PNext                 unsafe.Pointer
	Flags                 uint32
	Surface               SurfaceKHR
	MinImageCount         uint32
	ImageFormat           uint32
	ImageColorSpace       uint32
	ImageExtent           Extent2D
	ImageArrayLayers      uint32
	ImageUsage            uint32
	ImageSharingMode      uint32
	QueueFamilyIndexCount uint32
	PQueueFamilyIndices   uintptr
	PreTransform          uint32
	CompositeAlpha        uint32
	PresentMode           uint32
	Clipped               uint32
	OldSwapchain          SwapchainKHR
}

// PresentInfoKHR is VkPresentInfoKHR.
type PresentInfoKHR struct {
	SType              StructureType
	PNext              unsafe.Pointer
	WaitSemaphoreCount uint32
	PWaitSemaphores    *Semaphore
	SwapchainCount     uint32
	PSwapchains        *SwapchainKHR
	PImageIndices      *uint32
	PResults           uintptr
}

// SemaphoreCreateInfo is VkSemaphoreCreateInfo.
type SemaphoreCreateInfo struct {
	SType StructureType
	PNext unsafe.Pointer
	Flags uint32
}

// CreateXlibSurface is vkCreateXlibSurfaceKHR on an X11 window (the instance
// made with ExtSurface and ExtXlibSurface).
func CreateXlibSurface(instance Instance, display uintptr, window uint64) (SurfaceKHR, error) {
	fn := procAddr(instance, "vkCreateXlibSurfaceKHR")
	if fn == 0 {
		return 0, fmt.Errorf("vk: vkCreateXlibSurfaceKHR is not available")
	}
	info := &XlibSurfaceCreateInfoKHR{SType: StructureTypeXlibSurfaceCreateInfoKHR, Dpy: display, Window: window}
	var pinner runtime.Pinner
	defer pinner.Unpin()
	pinner.Pin(info)
	var s SurfaceKHR
	r, _, _ := purego.SyscallN(fn, uintptr(instance), uintptr(unsafe.Pointer(info)), 0, uintptr(unsafe.Pointer(&s)))
	return s, result(r)
}

// DestroySurface is vkDestroySurfaceKHR.
func DestroySurface(instance Instance, s SurfaceKHR) {
	if fn := procAddr(instance, "vkDestroySurfaceKHR"); fn != 0 {
		purego.SyscallN(fn, uintptr(instance), uintptr(s), 0)
	}
}

// SurfaceSupported is vkGetPhysicalDeviceSurfaceSupportKHR: whether the
// queue family can present to the surface.
func SurfaceSupported(instance Instance, physical PhysicalDevice, family uint32, s SurfaceKHR) (bool, error) {
	fn := procAddr(instance, "vkGetPhysicalDeviceSurfaceSupportKHR")
	if fn == 0 {
		return false, fmt.Errorf("vk: vkGetPhysicalDeviceSurfaceSupportKHR is not available")
	}
	var ok uint32
	r, _, _ := purego.SyscallN(fn, uintptr(physical), uintptr(family), uintptr(s), uintptr(unsafe.Pointer(&ok)))
	return ok != 0, result(r)
}

// SurfaceCapabilities is vkGetPhysicalDeviceSurfaceCapabilitiesKHR.
func SurfaceCapabilities(instance Instance, physical PhysicalDevice, s SurfaceKHR) (SurfaceCapabilitiesKHR, error) {
	var c SurfaceCapabilitiesKHR
	fn := procAddr(instance, "vkGetPhysicalDeviceSurfaceCapabilitiesKHR")
	if fn == 0 {
		return c, fmt.Errorf("vk: vkGetPhysicalDeviceSurfaceCapabilitiesKHR is not available")
	}
	r, _, _ := purego.SyscallN(fn, uintptr(physical), uintptr(s), uintptr(unsafe.Pointer(&c)))
	return c, result(r)
}

// SurfaceFormats is vkGetPhysicalDeviceSurfaceFormatsKHR.
func SurfaceFormats(instance Instance, physical PhysicalDevice, s SurfaceKHR) ([]SurfaceFormatKHR, error) {
	fn := procAddr(instance, "vkGetPhysicalDeviceSurfaceFormatsKHR")
	if fn == 0 {
		return nil, fmt.Errorf("vk: vkGetPhysicalDeviceSurfaceFormatsKHR is not available")
	}
	var n uint32
	if r, _, _ := purego.SyscallN(fn, uintptr(physical), uintptr(s), uintptr(unsafe.Pointer(&n)), 0); result(r) != nil {
		return nil, result(r)
	}
	out := make([]SurfaceFormatKHR, n)
	if n == 0 {
		return out, nil
	}
	r, _, _ := purego.SyscallN(fn, uintptr(physical), uintptr(s), uintptr(unsafe.Pointer(&n)), uintptr(unsafe.Pointer(&out[0])))
	return out[:n], result(r)
}

func (d *Device) loadExt() error {
	if d.ext.loaded {
		return nil
	}
	for _, f := range []struct {
		name string
		p    *uintptr
	}{
		{"vkCreateSwapchainKHR", &d.ext.createSwapchain},
		{"vkDestroySwapchainKHR", &d.ext.destroySwapchain},
		{"vkGetSwapchainImagesKHR", &d.ext.getSwapchainImages},
		{"vkAcquireNextImageKHR", &d.ext.acquireNextImage},
		{"vkQueuePresentKHR", &d.ext.queuePresent},
		{"vkCreateSemaphore", &d.ext.createSemaphore},
		{"vkDestroySemaphore", &d.ext.destroySemaphore},
		{"vkCmdBlitImage", &d.ext.cmdBlitImage},
	} {
		n := cstr(f.name)
		*f.p, _, _ = purego.SyscallN(d.get, d.handle, uintptr(unsafe.Pointer(n)))
		runtime.KeepAlive(n)
		if *f.p == 0 {
			return fmt.Errorf("vk: the device has no %s (made without %s?)", f.name, ExtSwapchain)
		}
	}
	d.ext.loaded = true
	return nil
}

// CreateSwapchain is vkCreateSwapchainKHR (the device made with ExtSwapchain).
func (d *Device) CreateSwapchain(info *SwapchainCreateInfoKHR) (SwapchainKHR, error) {
	if err := d.loadExt(); err != nil {
		return 0, err
	}
	info.SType = StructureTypeSwapchainCreateInfoKHR
	var pinner runtime.Pinner
	defer pinner.Unpin()
	pinner.Pin(info)
	var s SwapchainKHR
	r, _, _ := purego.SyscallN(d.ext.createSwapchain, d.handle, uintptr(unsafe.Pointer(info)), 0, uintptr(unsafe.Pointer(&s)))
	return s, result(r)
}

// DestroySwapchain is vkDestroySwapchainKHR.
func (d *Device) DestroySwapchain(s SwapchainKHR) {
	if d.loadExt() == nil {
		purego.SyscallN(d.ext.destroySwapchain, d.handle, uintptr(s), 0)
	}
}

// SwapchainImages is vkGetSwapchainImagesKHR.
func (d *Device) SwapchainImages(s SwapchainKHR) ([]Image, error) {
	if err := d.loadExt(); err != nil {
		return nil, err
	}
	var n uint32
	if r, _, _ := purego.SyscallN(d.ext.getSwapchainImages, d.handle, uintptr(s), uintptr(unsafe.Pointer(&n)), 0); result(r) != nil {
		return nil, result(r)
	}
	out := make([]Image, n)
	r, _, _ := purego.SyscallN(d.ext.getSwapchainImages, d.handle, uintptr(s), uintptr(unsafe.Pointer(&n)), uintptr(unsafe.Pointer(&out[0])))
	return out[:n], result(r)
}

// AcquireNextImage is vkAcquireNextImageKHR without a time limit, signalling
// the semaphore: the image's index. SuboptimalKHR is returned with a valid
// index; ErrorOutOfDateKHR means the swapchain must be made anew.
func (d *Device) AcquireNextImage(s SwapchainKHR, signal Semaphore) (uint32, error) {
	if err := d.loadExt(); err != nil {
		return 0, err
	}
	var i uint32
	r, _, _ := purego.SyscallN(d.ext.acquireNextImage, d.handle, uintptr(s), ^uintptr(0), uintptr(signal), 0, uintptr(unsafe.Pointer(&i)))
	if Result(int32(r)) == SuboptimalKHR {
		return i, SuboptimalKHR
	}
	return i, result(r)
}

// QueuePresent is vkQueuePresentKHR of one image, after the semaphore.
func (d *Device) QueuePresent(q Queue, s SwapchainKHR, index uint32, wait Semaphore) error {
	if err := d.loadExt(); err != nil {
		return err
	}
	var pinner runtime.Pinner
	defer pinner.Unpin()
	sw, idx, sem := new(SwapchainKHR), new(uint32), new(Semaphore)
	*sw, *idx, *sem = s, index, wait
	for _, p := range []any{sw, idx, sem} {
		pinner.Pin(p)
	}
	info := &PresentInfoKHR{SType: StructureTypePresentInfoKHR, SwapchainCount: 1, PSwapchains: sw, PImageIndices: idx}
	if wait != 0 {
		info.WaitSemaphoreCount, info.PWaitSemaphores = 1, sem
	}
	pinner.Pin(info)
	r, _, _ := purego.SyscallN(d.ext.queuePresent, uintptr(q), uintptr(unsafe.Pointer(info)))
	if Result(int32(r)) == SuboptimalKHR {
		return SuboptimalKHR
	}
	return result(r)
}

// CreateSemaphore is vkCreateSemaphore (binary).
func (d *Device) CreateSemaphore() (Semaphore, error) {
	if err := d.loadExt(); err != nil {
		return 0, err
	}
	info := &SemaphoreCreateInfo{SType: StructureTypeSemaphoreCreateInfo}
	var pinner runtime.Pinner
	defer pinner.Unpin()
	pinner.Pin(info)
	var s Semaphore
	r, _, _ := purego.SyscallN(d.ext.createSemaphore, d.handle, uintptr(unsafe.Pointer(info)), 0, uintptr(unsafe.Pointer(&s)))
	return s, result(r)
}

// DestroySemaphore is vkDestroySemaphore.
func (d *Device) DestroySemaphore(s Semaphore) {
	if d.loadExt() == nil {
		purego.SyscallN(d.ext.destroySemaphore, d.handle, uintptr(s), 0)
	}
}

// QueueSubmitSync is vkQueueSubmit of command buffers that wait for one
// semaphore (at the given stage; 0: none), signal another (0: none) and the
// fence.
func (d *Device) QueueSubmitSync(q Queue, f Fence, wait Semaphore, waitStage uint32, signal Semaphore, cbs ...CommandBuffer) error {
	var pinner runtime.Pinner
	defer pinner.Unpin()
	info := &SubmitInfo{SType: StructureTypeSubmitInfo}
	if len(cbs) > 0 {
		pinner.Pin(&cbs[0])
		info.CommandBufferCount, info.PCommandBuffers = uint32(len(cbs)), &cbs[0]
	}
	w, st, sg := new(Semaphore), new(uint32), new(Semaphore)
	*w, *st, *sg = wait, waitStage, signal
	for _, p := range []any{w, st, sg} {
		pinner.Pin(p)
	}
	if wait != 0 {
		info.WaitSemaphoreCount, info.PWaitSemaphores, info.PWaitDstStageMask = 1, uintptr(unsafe.Pointer(w)), uintptr(unsafe.Pointer(st))
	}
	if signal != 0 {
		info.SignalSemaphoreCount, info.PSignalSemaphores = 1, uintptr(unsafe.Pointer(sg))
	}
	pinner.Pin(info)
	r, _, _ := purego.SyscallN(d.fn.queueSubmit, uintptr(q), 1, uintptr(unsafe.Pointer(info)), uintptr(f))
	return result(r)
}

// ImageBlit is VkImageBlit: a source region (two corners) scaled into a
// destination one.
type ImageBlit struct {
	SrcSubresource ImageSubresourceLayers
	SrcOffsets     [2]Offset3D
	DstSubresource ImageSubresourceLayers
	DstOffsets     [2]Offset3D
}

// CmdBlitImage is vkCmdBlitImage of one region (converting the formats —
// the screen's RGBA into a swapchain's BGRA).
func (d *Device) CmdBlitImage(cb CommandBuffer, src Image, srcLayout uint32, dst Image, dstLayout uint32, region ImageBlit, filter uint32) {
	if d.loadExt() != nil {
		return
	}
	var pinner runtime.Pinner
	defer pinner.Unpin()
	r := new(ImageBlit)
	*r = region
	pinner.Pin(r)
	purego.SyscallN(d.ext.cmdBlitImage, uintptr(cb), uintptr(src), uintptr(srcLayout), uintptr(dst), uintptr(dstLayout), 1, uintptr(unsafe.Pointer(r)), uintptr(filter))
}

// SurfacePresentModes is vkGetPhysicalDeviceSurfacePresentModesKHR (FIFO is
// always among them).
func SurfacePresentModes(instance Instance, physical PhysicalDevice, s SurfaceKHR) ([]uint32, error) {
	fn := procAddr(instance, "vkGetPhysicalDeviceSurfacePresentModesKHR")
	if fn == 0 {
		return nil, fmt.Errorf("vk: vkGetPhysicalDeviceSurfacePresentModesKHR is not available")
	}
	var n uint32
	if r, _, _ := purego.SyscallN(fn, uintptr(physical), uintptr(s), uintptr(unsafe.Pointer(&n)), 0); result(r) != nil {
		return nil, result(r)
	}
	out := make([]uint32, n)
	if n == 0 {
		return out, nil
	}
	r, _, _ := purego.SyscallN(fn, uintptr(physical), uintptr(s), uintptr(unsafe.Pointer(&n)), uintptr(unsafe.Pointer(&out[0])))
	return out[:n], result(r)
}
