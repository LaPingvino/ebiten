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

	"github.com/hajimehoshi/ebiten/v2/internal/graphics"
	"github.com/hajimehoshi/ebiten/v2/internal/graphicsdriver"
	"github.com/hajimehoshi/ebiten/v2/internal/graphicsdriver/vulkan/vk"
)

// Image is an RGBA8 (premultiplied) image on the device, with the layout it
// was last left in.
type Image struct {
	g      *Graphics
	id     graphicsdriver.ImageID
	width  int
	height int
	image  vk.Image
	mem    vk.DeviceMemory
	view   vk.ImageView // made on first draw
	layout uint32
}

// NewImage is an image of Ebitengine's internal size for width × height
// (graphics.InternalImageSize: a power of two, at least 16), as the other
// drivers make them — the projection Ebitengine sends is for that size.
func (g *Graphics) NewImage(width, height int) (graphicsdriver.Image, error) {
	return g.newImage(graphics.InternalImageSize(width), graphics.InternalImageSize(height))
}

// newImage is an image of exactly width × height.
func (g *Graphics) newImage(width, height int) (*Image, error) {
	img, err := g.device.CreateImage2D(uint32(width), uint32(height), vk.FormatR8G8B8A8Unorm,
		vk.ImageUsageTransferSrcBit|vk.ImageUsageTransferDstBit|vk.ImageUsageSampledBit|vk.ImageUsageColorAttachmentBit)
	if err != nil {
		return nil, fmt.Errorf("vulkan: %w", err)
	}
	req := g.device.ImageMemoryRequirements(img)
	typ, ok := g.memProps.FindMemoryType(req.MemoryTypeBits, vk.MemoryPropertyDeviceLocalBit)
	if !ok {
		if typ, ok = g.memProps.FindMemoryType(req.MemoryTypeBits, 0); !ok {
			g.device.DestroyImage(img)
			return nil, fmt.Errorf("vulkan: no memory type for a %d×%d image", width, height)
		}
	}
	mem, err := g.device.AllocateMemory(req.Size, typ)
	if err != nil {
		g.device.DestroyImage(img)
		return nil, fmt.Errorf("vulkan: %w", err)
	}
	if err := g.device.BindImageMemory(img, mem, 0); err != nil {
		g.device.FreeMemory(mem)
		g.device.DestroyImage(img)
		return nil, fmt.Errorf("vulkan: %w", err)
	}
	g.nextImageID++
	i := &Image{g: g, id: g.nextImageID, width: width, height: height, image: img, mem: mem, layout: vk.ImageLayoutUndefined}
	g.images[i.id] = i
	return i, nil
}

func (i *Image) ID() graphicsdriver.ImageID { return i.id }

// Dispose releases the image once the batches using it are done.
func (i *Image) Dispose() {
	if i.image == 0 {
		return
	}
	g, img, mem, view := i.g, i.image, i.mem, i.view
	i.image, i.mem, i.view = 0, 0, 0
	delete(g.images, i.id)
	free := func() {
		if view != 0 {
			g.device.DestroyImageView(view)
		}
		g.device.DestroyImage(img)
		g.device.FreeMemory(mem)
	}
	g.release(free)
}

// transition records a barrier moving the image to layout, after and before
// any work (PARTIAL: all-commands, all-memory scopes; narrower ones later).
func (i *Image) transition(cb vk.CommandBuffer, layout uint32) {
	if i.layout == layout {
		return
	}
	i.g.closeRendering(cb) // no layout change inside a rendering pass
	i.g.device.CmdImageBarrier(cb, vk.ImageMemoryBarrier2{
		SrcStageMask: vk.PipelineStage2AllCommands, SrcAccessMask: vk.Access2MemoryWrite,
		DstStageMask: vk.PipelineStage2AllCommands, DstAccessMask: vk.Access2MemoryRead | vk.Access2MemoryWrite,
		OldLayout: i.layout, NewLayout: layout,
		SrcQueueFamilyIndex: vk.QueueFamilyIgnored, DstQueueFamilyIndex: vk.QueueFamilyIgnored,
		Image: i.image, SubresourceRange: vk.ColorRange,
	})
	i.layout = layout
}

func regionCopy(offset uint64, r [4]int) vk.BufferImageCopy {
	return vk.BufferImageCopy{
		BufferOffset:     offset,
		ImageSubresource: vk.ImageSubresourceLayers{AspectMask: vk.ImageAspectColorBit, LayerCount: 1},
		ImageOffset:      vk.Offset3D{X: int32(r[0]), Y: int32(r[1])},
		ImageExtent:      vk.Extent3D{Width: uint32(r[2] - r[0]), Height: uint32(r[3] - r[1]), Depth: 1},
	}
}

// WritePixels copies the regions' pixels into a staging buffer and records
// their copies into the image, in the current command stream.
func (i *Image) WritePixels(args []graphicsdriver.PixelsArgs) error {
	if len(args) == 0 {
		return nil
	}
	var size uint64
	for _, a := range args {
		size += uint64(len(a.Pixels))
	}
	g := i.g
	staging, err := g.newHostBuffer(size, vk.BufferUsageTransferSrcBit)
	if err != nil {
		return err
	}
	cb, err := g.commandBuffer()
	if err != nil {
		g.freeHostBuffer(staging)
		return err
	}
	g.afterBatch = append(g.afterBatch, func() { g.freeHostBuffer(staging) })
	var copies []vk.BufferImageCopy
	var off uint64
	for _, a := range args {
		copy(staging.mapped[off:], a.Pixels)
		r := a.Region
		copies = append(copies, regionCopy(off, [4]int{r.Min.X, r.Min.Y, r.Max.X, r.Max.Y}))
		off += uint64(len(a.Pixels))
	}
	if i.layout == vk.ImageLayoutUndefined && !coversWhole(i, args) {
		// The rest of a new image is transparent black, as in the other drivers.
		g.closeRendering(cb)
		i.transition(cb, vk.ImageLayoutTransferDstOptimal)
		g.device.CmdClearColorImage(cb, i.image, vk.ImageLayoutTransferDstOptimal, [4]float32{})
	}
	i.transition(cb, vk.ImageLayoutTransferDstOptimal)
	g.closeRendering(cb)
	g.device.CmdCopyBufferToImage(cb, staging.buf, i.image, vk.ImageLayoutTransferDstOptimal, copies...)
	return nil
}

func coversWhole(i *Image, args []graphicsdriver.PixelsArgs) bool {
	for _, a := range args {
		if a.Region.Min.X == 0 && a.Region.Min.Y == 0 && a.Region.Max.X == i.width && a.Region.Max.Y == i.height {
			return true
		}
	}
	return false
}

// ReadPixelsAsync records the regions' copies into a buffer at the current
// position of the command stream; the read-back completes with the batch.
func (i *Image) ReadPixelsAsync(args []graphicsdriver.PixelsArgs) (graphicsdriver.PixelReadback, error) {
	var size uint64
	for _, a := range args {
		size += uint64(4 * a.Region.Dx() * a.Region.Dy())
	}
	g := i.g
	r := &readback{g: g, args: args, serial: g.serial}
	if size == 0 {
		r.done = true
		return r, nil
	}
	buf, err := g.newHostBuffer(size, vk.BufferUsageTransferDstBit)
	if err != nil {
		return nil, err
	}
	r.buf = buf
	cb, err := g.commandBuffer()
	if err != nil {
		r.Dispose()
		return nil, err
	}
	if i.layout == vk.ImageLayoutUndefined { // never written: transparent black
		g.closeRendering(cb)
		i.transition(cb, vk.ImageLayoutTransferDstOptimal)
		g.device.CmdClearColorImage(cb, i.image, vk.ImageLayoutTransferDstOptimal, [4]float32{})
	}
	var copies []vk.BufferImageCopy
	var off uint64
	for _, a := range args {
		rg := a.Region
		copies = append(copies, regionCopy(off, [4]int{rg.Min.X, rg.Min.Y, rg.Max.X, rg.Max.Y}))
		off += uint64(4 * rg.Dx() * rg.Dy())
	}
	i.transition(cb, vk.ImageLayoutTransferSrcOptimal)
	g.closeRendering(cb)
	g.device.CmdCopyImageToBuffer(cb, i.image, vk.ImageLayoutTransferSrcOptimal, buf.buf, copies...)
	// The host reads the buffer after the batch: QueueWaitIdle makes the
	// device's writes visible to it (host-coherent memory).
	return r, nil
}

// readback is a read-back in flight: its buffer, filled when its batch has
// completed.
type readback struct {
	g      *Graphics
	args   []graphicsdriver.PixelsArgs
	buf    *hostBuffer
	serial uint64
	done   bool
}

func (r *readback) copyOut() {
	var off int
	for _, a := range r.args {
		n := 4 * a.Region.Dx() * a.Region.Dy()
		copy(a.Pixels, r.buf.mapped[off:off+n])
		off += n
	}
}

func (r *readback) Poll() (bool, error) {
	if r.done {
		return true, nil
	}
	if !r.g.retireUpTo(r.serial, false) { // its batch's fence, not waited for
		return false, nil
	}
	r.copyOut()
	r.done = true
	return true, nil
}

func (r *readback) Wait() error {
	if r.done {
		return nil
	}
	if r.g.completed < r.serial {
		if r.g.recording && r.g.serial == r.serial { // its batch not submitted yet
			if err := r.g.flush(); err != nil {
				return err
			}
		}
		if !r.g.retireUpTo(r.serial, true) {
			return errors.New("vulkan: the read-back's batch failed")
		}
	}
	r.copyOut()
	r.done = true
	return nil
}

func (r *readback) Dispose() {
	if r.buf == nil {
		return
	}
	b, g := r.buf, r.g
	r.buf = nil
	if g.completed < r.serial { // its batch may still be writing it
		g.release(func() { g.freeHostBuffer(b) })
		return
	}
	g.freeHostBuffer(b)
}
