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
	"fmt"

	"github.com/hajimehoshi/ebiten/v2/internal/graphicsdriver/vulkan/vk"
)

// Images share device memory (LOOP V7): Vulkan caps the number of
// allocations (maxMemoryAllocationCount, often 4096) and each one costs, so
// images are bound at offsets inside larger blocks, one list of blocks per
// memory type. Only optimal-tiling images live in the blocks, so
// bufferImageGranularity never applies. An image larger than half a block
// gets an allocation of its own. A block left empty is freed, but one empty
// block per memory type is kept for the next image.

// memBlockSize is the size of a shared block (a variable for the tests).
var memBlockSize uint64 = 64 << 20

// span is a free range in a block.
type span struct{ off, size uint64 }

// spans is a block's free list, sorted by offset, neighbours merged.
type spans []span

// take finds the first free range holding size at align and removes it
// (keeping what is left before and after); ok false when none fits.
func (s *spans) take(size, align uint64) (off uint64, ok bool) {
	for i, f := range *s {
		start := (f.off + align - 1) / align * align
		if start+size > f.off+f.size {
			continue
		}
		var rest []span
		if start > f.off {
			rest = append(rest, span{f.off, start - f.off})
		}
		if end := start + size; end < f.off+f.size {
			rest = append(rest, span{end, f.off + f.size - end})
		}
		*s = append((*s)[:i], append(rest, (*s)[i+1:]...)...)
		return start, true
	}
	return 0, false
}

// give returns a range, merging it with free neighbours.
func (s *spans) give(off, size uint64) {
	i := 0
	for i < len(*s) && (*s)[i].off < off {
		i++
	}
	*s = append((*s)[:i], append([]span{{off, size}}, (*s)[i:]...)...)
	if i+1 < len(*s) && (*s)[i].off+(*s)[i].size == (*s)[i+1].off {
		(*s)[i].size += (*s)[i+1].size
		*s = append((*s)[:i+1], (*s)[i+2:]...)
	}
	if i > 0 && (*s)[i-1].off+(*s)[i-1].size == (*s)[i].off {
		(*s)[i-1].size += (*s)[i].size
		*s = append((*s)[:i], (*s)[i+1:]...)
	}
}

// empty reports whether the whole block of size is free.
func (s spans) empty(size uint64) bool { return len(s) == 1 && s[0].off == 0 && s[0].size == size }

type memBlock struct {
	mem  vk.DeviceMemory
	size uint64
	free spans
}

// memAlloc is a piece of device memory an image is bound to: a range of a
// shared block, or (block nil) an allocation of its own.
type memAlloc struct {
	mem   vk.DeviceMemory
	off   uint64
	size  uint64
	typ   uint32
	block *memBlock
}

// allocImageMemory finds memory of type typ for req.
func (g *Graphics) allocImageMemory(req vk.MemoryRequirements, typ uint32) (memAlloc, error) {
	if req.Size > memBlockSize/2 {
		mem, err := g.device.AllocateMemory(req.Size, typ)
		if err != nil {
			return memAlloc{}, err
		}
		return memAlloc{mem: mem, size: req.Size, typ: typ}, nil
	}
	align := max(req.Alignment, 1)
	for _, b := range g.memBlocks[typ] {
		if off, ok := b.free.take(req.Size, align); ok {
			return memAlloc{mem: b.mem, off: off, size: req.Size, typ: typ, block: b}, nil
		}
	}
	mem, err := g.device.AllocateMemory(memBlockSize, typ)
	if err != nil {
		return memAlloc{}, err
	}
	b := &memBlock{mem: mem, size: memBlockSize, free: spans{{0, memBlockSize}}}
	if g.memBlocks == nil {
		g.memBlocks = map[uint32][]*memBlock{}
	}
	g.memBlocks[typ] = append(g.memBlocks[typ], b)
	off, ok := b.free.take(req.Size, align)
	if !ok {
		return memAlloc{}, fmt.Errorf("vulkan: %d bytes don't fit a new %d-byte block", req.Size, memBlockSize)
	}
	return memAlloc{mem: mem, off: off, size: req.Size, typ: typ, block: b}, nil
}

// freeImageMemory gives a's memory back (called once the device is done
// with the image bound to it).
func (g *Graphics) freeImageMemory(a memAlloc) {
	if a.block == nil {
		g.device.FreeMemory(a.mem)
		return
	}
	a.block.free.give(a.off, a.size)
	if !a.block.free.empty(a.block.size) {
		return
	}
	blocks := g.memBlocks[a.typ]
	empties := 0
	for _, b := range blocks {
		if b.free.empty(b.size) {
			empties++
		}
	}
	if empties < 2 { // keep one empty block for the next image
		return
	}
	for i, b := range blocks {
		if b == a.block {
			g.device.FreeMemory(b.mem)
			g.memBlocks[a.typ] = append(blocks[:i], blocks[i+1:]...)
			return
		}
	}
}

// freeMemBlocks frees every block (destroy, after every image).
func (g *Graphics) freeMemBlocks() {
	for _, bs := range g.memBlocks {
		for _, b := range bs {
			g.device.FreeMemory(b.mem)
		}
	}
	g.memBlocks = nil
}
