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
	"slices"

	"github.com/hajimehoshi/ebiten/v2/internal/glfw"
	"github.com/hajimehoshi/ebiten/v2/internal/graphicsdriver/vulkan/vk"
)

// presenting is the window's side of the driver: the X11 window's surface,
// the swapchain the screen is shown through, and the semaphores between
// acquiring, drawing and presenting. PARTIAL: X11 only (Wayland next).
type presenting struct {
	hasSurfaceExt, hasSwapchainExt bool
	window                         uintptr // the X11 window SetWindow gave (its surface made at the first present)
	surfaceTried                   bool
	surface                        vk.SurfaceKHR
	swapchain                      vk.SwapchainKHR
	swapImages                     []vk.Image
	swapLayouts                    []uint32
	swapFormat                     uint32
	swapW, swapH                   int
	swapStale                      bool
	vsyncOff                       bool                         // SetVsyncEnabled(false): MAILBOX, else IMMEDIATE, else FIFO
	presentMode                    uint32                       // the swapchain's                         // rebuilt before the next present (out of date / suboptimal)
	acquired                       [framesInFlight]vk.Semaphore // per batch slot: the image acquired for it
	rendered                       []vk.Semaphore               // per swapchain image: drawn, may be presented
	submitWait                     vk.Semaphore                 // the next submit's wait (0: none)
	submitWaitStage                uint32
	submitSignal                   vk.Semaphore // the next submit's signal (0: none)
}

// SetWindow takes the game's X11 window (ui hands drivers their native
// window — before Initialize: the surface waits for the first present).
func (g *Graphics) SetWindow(window uintptr) {
	g.window = window
}

// makeSurface is a Vulkan surface on the window, when the device's queue can
// present there (once; without one the screen is drawn and not shown).
func (g *Graphics) makeSurface() {
	g.surfaceTried = true
	window := g.window
	if !g.hasSurfaceExt || !g.hasSwapchainExt || window == 0 {
		return
	}
	dpy, err := glfw.GetX11Display()
	if err != nil || dpy == 0 {
		return
	}
	s, err := vk.CreateXlibSurface(g.instance, dpy, uint64(window))
	if err != nil {
		return
	}
	if ok, err := vk.SurfaceSupported(g.instance, g.physical, g.family, s); err != nil || !ok {
		vk.DestroySurface(g.instance, s)
		return
	}
	g.surface = s
}

// makeSwapchain (re)creates the swapchain at the screen's size: B8G8R8A8 /
// R8G8B8A8 UNORM where offered, FIFO, opaque, transfer destination (the
// screen is blitted in).
func (g *Graphics) makeSwapchain() error {
	_ = g.device.WaitIdle()
	g.retireUpTo(g.serial, true)
	caps, err := vk.SurfaceCapabilities(g.instance, g.physical, g.surface)
	if err != nil {
		return fmt.Errorf("vulkan: %w", err)
	}
	formats, err := vk.SurfaceFormats(g.instance, g.physical, g.surface)
	if err != nil || len(formats) == 0 {
		return fmt.Errorf("vulkan: no surface formats (%v)", err)
	}
	format := formats[0]
	for _, f := range formats {
		if f.Format == vk.FormatB8G8R8A8Unorm || f.Format == vk.FormatR8G8B8A8Unorm {
			format = f
			break
		}
	}
	mode := vk.PresentModeFifoKHR // vsync: always there
	if g.vsyncOff {
		modes, _ := vk.SurfacePresentModes(g.instance, g.physical, g.surface)
		for _, want := range []uint32{vk.PresentModeMailboxKHR, vk.PresentModeImmediateKHR} {
			if slices.Contains(modes, want) {
				mode = want
				break
			}
		}
	}
	n := max(caps.MinImageCount+1, 2)
	if caps.MaxImageCount != 0 {
		n = min(n, caps.MaxImageCount)
	}
	w, h := uint32(g.screen.width), uint32(g.screen.height)
	if caps.CurrentExtent.Width != 0xFFFFFFFF { // the window's size rules
		w, h = caps.CurrentExtent.Width, caps.CurrentExtent.Height
	}
	if w == 0 || h == 0 {
		return errors.New("vulkan: the window has no size")
	}
	old := g.swapchain
	sc, err := g.device.CreateSwapchain(&vk.SwapchainCreateInfoKHR{
		Surface: g.surface, MinImageCount: n, ImageFormat: format.Format, ImageColorSpace: format.ColorSpace,
		ImageExtent: vk.Extent2D{Width: w, Height: h}, ImageArrayLayers: 1,
		ImageUsage: vk.ImageUsageTransferDstBit | vk.ImageUsageColorAttachmentBit, ImageSharingMode: vk.SharingModeExclusive,
		PreTransform: caps.CurrentTransform, CompositeAlpha: vk.CompositeAlphaOpaqueBitKHR, PresentMode: mode,
		Clipped: 1, OldSwapchain: old,
	})
	if old != 0 {
		g.device.DestroySwapchain(old)
	}
	if err != nil {
		g.swapchain = 0
		return fmt.Errorf("vulkan: %w", err)
	}
	g.swapchain, g.swapFormat, g.swapW, g.swapH, g.swapStale, g.presentMode = sc, format.Format, int(w), int(h), false, mode
	if g.swapImages, err = g.device.SwapchainImages(sc); err != nil {
		return fmt.Errorf("vulkan: %w", err)
	}
	g.swapLayouts = make([]uint32, len(g.swapImages))
	for _, s := range g.rendered {
		g.device.DestroySemaphore(s)
	}
	g.rendered = g.rendered[:0]
	for range g.swapImages {
		s, err := g.device.CreateSemaphore()
		if err != nil {
			return fmt.Errorf("vulkan: %w", err)
		}
		g.rendered = append(g.rendered, s)
	}
	for i := range g.acquired {
		if g.acquired[i] == 0 {
			if g.acquired[i], err = g.device.CreateSemaphore(); err != nil {
				return fmt.Errorf("vulkan: %w", err)
			}
		}
	}
	return nil
}

// present is End(FlushModePresent) with a window: a swapchain image
// acquired (for this batch slot), the screen blitted into it (its size to
// the image's), the batch submitted waiting for the acquire and signalling
// the image's semaphore, the image presented; an out-of-date swapchain is
// rebuilt on the next frame (this one only submitted).
func (g *Graphics) present() error {
	cb, err := g.commandBuffer() // the slot retired: its acquire semaphore free again
	if err != nil {
		return err
	}
	if g.swapchain == 0 || g.swapStale || g.swapW != g.screen.width || g.swapH != g.screen.height {
		if err := g.flush(); err != nil {
			return err
		}
		if err := g.makeSwapchain(); err != nil {
			return nil // nothing to show this frame (a minimised window, say)
		}
		if cb, err = g.commandBuffer(); err != nil {
			return err
		}
	}
	sem := g.acquired[g.cur]
	idx, err := g.device.AcquireNextImage(g.swapchain, sem)
	if errors.Is(err, vk.SuboptimalKHR) {
		g.swapStale, err = true, nil
	}
	if err != nil {
		g.swapStale = true
		return g.flush()
	}
	img := g.swapImages[idx]
	g.screen.transition(cb, vk.ImageLayoutTransferSrcOptimal)
	g.device.CmdImageBarrier(cb, vk.ImageMemoryBarrier2{
		SrcStageMask: vk.PipelineStage2AllCommands, DstStageMask: vk.PipelineStage2AllCommands, DstAccessMask: vk.Access2MemoryWrite,
		OldLayout: vk.ImageLayoutUndefined, NewLayout: vk.ImageLayoutTransferDstOptimal,
		SrcQueueFamilyIndex: vk.QueueFamilyIgnored, DstQueueFamilyIndex: vk.QueueFamilyIgnored, Image: img, SubresourceRange: vk.ColorRange,
	})
	layers := vk.ImageSubresourceLayers{AspectMask: vk.ImageAspectColorBit, LayerCount: 1}
	g.closeRendering(cb)
	g.device.CmdBlitImage(cb, g.screen.image, vk.ImageLayoutTransferSrcOptimal, img, vk.ImageLayoutTransferDstOptimal, vk.ImageBlit{
		SrcSubresource: layers, SrcOffsets: [2]vk.Offset3D{{}, {X: int32(g.screen.width), Y: int32(g.screen.height), Z: 1}},
		DstSubresource: layers, DstOffsets: [2]vk.Offset3D{{}, {X: int32(g.swapW), Y: int32(g.swapH), Z: 1}},
	}, vk.FilterNearest)
	g.device.CmdImageBarrier(cb, vk.ImageMemoryBarrier2{
		SrcStageMask: vk.PipelineStage2AllCommands, SrcAccessMask: vk.Access2MemoryWrite, DstStageMask: vk.PipelineStage2AllCommands,
		OldLayout: vk.ImageLayoutTransferDstOptimal, NewLayout: vk.ImageLayoutPresentSrcKHR,
		SrcQueueFamilyIndex: vk.QueueFamilyIgnored, DstQueueFamilyIndex: vk.QueueFamilyIgnored, Image: img, SubresourceRange: vk.ColorRange,
	})
	const transferStage = 0x1000 // VK_PIPELINE_STAGE_TRANSFER_BIT: where the acquired image is first written
	g.submitWait, g.submitWaitStage, g.submitSignal = sem, transferStage, g.rendered[idx]
	err = g.flush()
	g.submitWait, g.submitWaitStage, g.submitSignal = 0, 0, 0
	if err != nil {
		return err
	}
	if err := g.device.QueuePresent(g.queue, g.swapchain, idx, g.rendered[idx]); err != nil {
		if errors.Is(err, vk.SuboptimalKHR) || errors.Is(err, vk.ErrorOutOfDateKHR) {
			g.swapStale = true
			return nil
		}
		return deviceError(err)
	}
	return nil
}

// destroyPresenting releases the swapchain, its semaphores and the surface
// (the device idle).
func (g *Graphics) destroyPresenting() {
	for _, s := range g.rendered {
		g.device.DestroySemaphore(s)
	}
	for i, s := range g.acquired {
		if s != 0 {
			g.device.DestroySemaphore(s)
			g.acquired[i] = 0
		}
	}
	if g.swapchain != 0 {
		g.device.DestroySwapchain(g.swapchain)
		g.swapchain = 0
	}
	if g.surface != 0 {
		vk.DestroySurface(g.instance, g.surface)
		g.surface = 0
	}
}

// SetVsyncEnabled chooses the present mode: FIFO with vsync; without, the
// lowest-latency mode the surface offers (MAILBOX, else IMMEDIATE). The
// swapchain is remade at the next present when it changes.
func (g *Graphics) SetVsyncEnabled(enabled bool) {
	if g.vsyncOff == !enabled {
		return
	}
	g.vsyncOff = !enabled
	if g.swapchain != 0 {
		g.swapStale = true
	}
}
