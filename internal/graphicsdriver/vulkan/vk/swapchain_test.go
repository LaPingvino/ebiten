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
	"os"
	"slices"
	"strings"
	"testing"
	"unsafe"

	"github.com/ebitengine/purego"

	"github.com/hajimehoshi/ebiten/v2/internal/graphicsdriver/vulkan/vk"
)

// x11 is the little of libX11 the test needs: a window to present to, and
// its pixels read back.
type x11 struct {
	openDisplay   func(name uintptr) uintptr
	closeDisplay  func(dpy uintptr) int32
	defaultScreen func(dpy uintptr) int32
	rootWindow    func(dpy uintptr, screen int32) uint64
	createWindow  func(dpy uintptr, parent uint64, x, y int32, w, h, border uint32, borderPixel, background uint64) uint64
	mapWindow     func(dpy uintptr, w uint64) int32
	destroyWindow func(dpy uintptr, w uint64) int32
	sync          func(dpy uintptr, discard int32) int32
	getImage      func(dpy uintptr, d uint64, x, y int32, w, h uint32, planes uint64, format int32) *xImage
	destroyImage  func(img *xImage) int32
}

// xImage is the head of Xlib's XImage: the fields before data.
type xImage struct {
	width, height, xoffset, format int32
	data                           *byte
}

func openX11(t *testing.T) (*x11, uintptr) {
	t.Helper()
	if os.Getenv("DISPLAY") == "" {
		t.Skip("no X display (run under xvfb-run)")
	}
	lib, err := purego.Dlopen("libX11.so.6", purego.RTLD_LAZY|purego.RTLD_GLOBAL)
	if err != nil {
		t.Skip(err)
	}
	x := &x11{}
	for name, fn := range map[string]any{
		"XOpenDisplay": &x.openDisplay, "XCloseDisplay": &x.closeDisplay, "XDefaultScreen": &x.defaultScreen,
		"XRootWindow": &x.rootWindow, "XCreateSimpleWindow": &x.createWindow, "XMapWindow": &x.mapWindow,
		"XDestroyWindow": &x.destroyWindow, "XSync": &x.sync, "XGetImage": &x.getImage, "XDestroyImage": &x.destroyImage,
	} {
		purego.RegisterLibFunc(fn, lib, name)
	}
	dpy := x.openDisplay(0)
	if dpy == 0 {
		t.Skip("XOpenDisplay failed")
	}
	t.Cleanup(func() { x.closeDisplay(dpy) })
	return x, dpy
}

// pixel is the window's pixel (x, y) as R, G, B (a 24/32-bit ZPixmap, BGRX).
func (x *x11) pixel(t *testing.T, dpy uintptr, win uint64, px, py int) [3]byte {
	img := x.getImage(dpy, win, int32(px), int32(py), 1, 1, ^uint64(0), 2) // AllPlanes, ZPixmap
	if img == nil {
		t.Fatal("XGetImage failed")
	}
	defer x.destroyImage(img)
	p := unsafe.Slice(img.data, 4)
	return [3]byte{p[2], p[1], p[0]}
}

// Presenting (V5's base): an X11 window, a Vulkan surface on it, a FIFO
// swapchain; an image acquired, cleared to orange, presented — and read
// back from the window: the X server shows the colour. Under the
// validation layer (its errors fail the test).
func TestSwapchainPresent(t *testing.T) {
	x, dpy := openX11(t)
	const w, h = 64, 48
	win := x.createWindow(dpy, x.rootWindow(dpy, x.defaultScreen(dpy)), 0, 0, w, h, 0, 0, 0)
	x.mapWindow(dpy, win)
	x.sync(dpy, 0)
	t.Cleanup(func() { x.destroyWindow(dpy, win) })

	if v, err := vk.EnumerateInstanceVersion(); err != nil || v < vk.APIVersion13 {
		t.Skipf("no Vulkan 1.3 loader (%v)", err)
	}
	exts := []string{vk.ExtSurface, vk.ExtXlibSurface, vk.ExtDebugUtils}
	instance, err := vk.CreateInstance("ebitengine-test", vk.APIVersion13, exts, []string{vk.LayerKhronosValidation})
	if errors.Is(err, vk.ErrorLayerNotPresent) || errors.Is(err, vk.ErrorExtensionNotPresent) {
		instance, err = vk.CreateInstance("ebitengine-test", vk.APIVersion13, exts[:2], nil)
	}
	if err != nil {
		t.Skip(err)
	}
	defer vk.DestroyInstance(instance)
	if m, err := vk.CreateDebugUtilsMessenger(instance, vk.DebugUtilsMessageSeverityErrorBitEXT|vk.DebugUtilsMessageSeverityWarningBitEXT,
		vk.DebugUtilsMessageTypeGeneralBitEXT|vk.DebugUtilsMessageTypeValidationBitEXT, func(m vk.DebugMessage) {
			if m.Severity&vk.DebugUtilsMessageSeverityErrorBitEXT != 0 && !strings.Contains(m.Text, "Loader") {
				t.Errorf("validation: %s: %s", m.ID, m.Text)
			}
		}); err == nil {
		defer vk.DestroyDebugUtilsMessenger(instance, m)
	}
	surface, err := vk.CreateXlibSurface(instance, dpy, win)
	if err != nil {
		t.Fatal(err)
	}
	defer vk.DestroySurface(instance, surface)

	devices, _ := vk.EnumeratePhysicalDevices(instance)
	var physical vk.PhysicalDevice
	family, found := uint32(0), false
	for _, d := range devices {
		for i, f := range vk.GetPhysicalDeviceQueueFamilyProperties(instance, d) {
			if ok, _ := vk.SurfaceSupported(instance, d, uint32(i), surface); ok && f.QueueFlags&vk.QueueGraphicsBit != 0 {
				physical, family, found = d, uint32(i), true
				break
			}
		}
		if found {
			break
		}
	}
	if !found {
		t.Skip("no device presents to this surface")
	}
	device, err := vk.CreateDevice(instance, physical, family, &vk.PhysicalDeviceVulkan13Features{Synchronization2: 1, DynamicRendering: 1}, []string{vk.ExtSwapchain})
	if err != nil {
		t.Fatal(err)
	}
	defer device.Destroy()
	queue := device.Queue(family, 0)

	caps, err := vk.SurfaceCapabilities(instance, physical, surface)
	if err != nil {
		t.Fatal(err)
	}
	formats, err := vk.SurfaceFormats(instance, physical, surface)
	if err != nil || len(formats) == 0 {
		t.Fatalf("surface formats: %v %v", formats, err)
	}
	modes, err := vk.SurfacePresentModes(instance, physical, surface)
	if err != nil || !slices.Contains(modes, vk.PresentModeFifoKHR) {
		t.Errorf("present modes %v (%v): FIFO must be among them", modes, err)
	}
	t.Logf("present modes %v", modes)
	format := formats[0]
	for _, f := range formats {
		if f.Format == vk.FormatB8G8R8A8Unorm {
			format = f
		}
	}
	n := max(caps.MinImageCount, 2)
	if caps.MaxImageCount != 0 {
		n = min(n, caps.MaxImageCount)
	}
	t.Logf("surface: %d–%d images, extent %v, %d formats (using %d)", caps.MinImageCount, caps.MaxImageCount, caps.CurrentExtent, len(formats), format.Format)
	sc, err := device.CreateSwapchain(&vk.SwapchainCreateInfoKHR{
		Surface: surface, MinImageCount: n, ImageFormat: format.Format, ImageColorSpace: format.ColorSpace,
		ImageExtent: vk.Extent2D{Width: w, Height: h}, ImageArrayLayers: 1,
		ImageUsage: vk.ImageUsageColorAttachmentBit | vk.ImageUsageTransferDstBit, ImageSharingMode: vk.SharingModeExclusive,
		PreTransform: caps.CurrentTransform, CompositeAlpha: vk.CompositeAlphaOpaqueBitKHR, PresentMode: vk.PresentModeFifoKHR, Clipped: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer device.DestroySwapchain(sc)
	images, err := device.SwapchainImages(sc)
	if err != nil {
		t.Fatal(err)
	}
	acquired, _ := device.CreateSemaphore()
	defer device.DestroySemaphore(acquired)
	rendered, _ := device.CreateSemaphore()
	defer device.DestroySemaphore(rendered)
	fence, _ := device.CreateFence(false)
	defer device.DestroyFence(fence)
	pool, _ := device.CreateCommandPool(family, 0)
	defer device.DestroyCommandPool(pool)
	cb, _ := device.AllocateCommandBuffer(pool)

	idx, err := device.AcquireNextImage(sc, acquired)
	if err != nil && !errors.Is(err, vk.SuboptimalKHR) {
		t.Fatal(err)
	}
	img := images[idx]
	_ = device.BeginCommandBuffer(cb, vk.CommandBufferUsageOneTimeSubmitBit)
	device.CmdImageBarrier(cb, vk.ImageMemoryBarrier2{
		SrcStageMask: vk.PipelineStage2AllTransfer, DstStageMask: vk.PipelineStage2AllTransfer, DstAccessMask: vk.Access2TransferWrite,
		OldLayout: vk.ImageLayoutUndefined, NewLayout: vk.ImageLayoutTransferDstOptimal,
		SrcQueueFamilyIndex: vk.QueueFamilyIgnored, DstQueueFamilyIndex: vk.QueueFamilyIgnored, Image: img, SubresourceRange: vk.ColorRange,
	})
	orange := [4]float32{1, 0.5, 0, 1}
	if format.Format == vk.FormatB8G8R8A8Unorm || format.Format == vk.FormatR8G8B8A8Unorm {
		device.CmdClearColorImage(cb, img, vk.ImageLayoutTransferDstOptimal, orange)
	} else {
		t.Skipf("surface format %d not a UNORM RGBA / BGRA one", format.Format)
	}
	device.CmdImageBarrier(cb, vk.ImageMemoryBarrier2{
		SrcStageMask: vk.PipelineStage2AllTransfer, SrcAccessMask: vk.Access2TransferWrite, DstStageMask: vk.PipelineStage2None,
		OldLayout: vk.ImageLayoutTransferDstOptimal, NewLayout: vk.ImageLayoutPresentSrcKHR,
		SrcQueueFamilyIndex: vk.QueueFamilyIgnored, DstQueueFamilyIndex: vk.QueueFamilyIgnored, Image: img, SubresourceRange: vk.ColorRange,
	})
	_ = device.EndCommandBuffer(cb)
	const transferStage = 0x1000 // VK_PIPELINE_STAGE_TRANSFER_BIT: the acquired image is written there
	if err := device.QueueSubmitSync(queue, fence, acquired, transferStage, rendered, cb); err != nil {
		t.Fatal(err)
	}
	if err := device.QueuePresent(queue, sc, idx, rendered); err != nil && !errors.Is(err, vk.SuboptimalKHR) {
		t.Fatal(err)
	}
	_ = device.WaitForFence(fence)
	_ = device.QueueWaitIdle(queue)
	x.sync(dpy, 0)
	got := x.pixel(t, dpy, win, w/2, h/2)
	t.Logf("presented image %d of %d; the window's centre pixel %v", idx, len(images), got)
	if got[0] < 250 || got[1] < 120 || got[1] > 135 || got[2] > 5 {
		t.Errorf("window pixel %v, want orange ≈ (255, 128, 0)", got)
	}
}
