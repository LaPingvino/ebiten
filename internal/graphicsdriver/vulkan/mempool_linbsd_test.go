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
	"image"
	"reflect"
	"testing"

	"github.com/hajimehoshi/ebiten/v2/internal/graphicsdriver"
)

// The free list: aligned first fit, what is left kept, neighbours merged
// when given back, whole again at the end.
func TestSpans(t *testing.T) {
	s := spans{{0, 1000}}
	a, _ := s.take(100, 1)
	b, _ := s.take(100, 256) // 100 is not aligned: starts at 256, leaving 100..256 free
	c, _ := s.take(50, 1)    // first fit: the gap before b
	if a != 0 || b != 256 || c != 100 {
		t.Fatalf("offsets %d %d %d", a, b, c)
	}
	if want := (spans{{150, 106}, {356, 644}}); !reflect.DeepEqual(s, want) {
		t.Fatalf("free %v, want %v", s, want)
	}
	if _, ok := s.take(700, 1); ok {
		t.Error("a range larger than any free one was given")
	}
	s.give(b, 100)
	s.give(a, 100)
	s.give(c, 50)
	if !s.empty(1000) {
		t.Errorf("after giving everything back: %v", s)
	}
}

// More images than one block holds: they spread over blocks, each keeps its
// own pixels, and disposing them all frees the blocks but one (validation
// silent).
func TestImagesShareMemory(t *testing.T) {
	old := memBlockSize
	memBlockSize = 1 << 20 // a 256×256 image is 256 KiB: under 4 a block
	t.Cleanup(func() { memBlockSize = old })
	g := testGraphics(t)
	var imgs []graphicsdriver.Image
	for n := 0; n < 10; n++ {
		img, err := g.NewImage(256, 256)
		if err != nil {
			t.Fatal(err)
		}
		v := byte(n * 20)
		if err := img.WritePixels([]graphicsdriver.PixelsArgs{{Pixels: pixels(256, 256, func(x, y int) [4]byte { return [4]byte{v, byte(x), byte(y), 255} }), Region: image.Rect(0, 0, 256, 256)}}); err != nil {
			t.Fatal(err)
		}
		imgs = append(imgs, img)
	}
	var blocks int
	for _, bs := range g.memBlocks {
		blocks += len(bs)
	}
	if blocks < 3 {
		t.Errorf("10 images of 256 KiB in %d blocks of 1 MiB", blocks)
	}
	for n, img := range imgs {
		got := read(t, img, image.Rect(10, 20, 12, 21))
		if want := []byte{byte(n * 20), 10, 20, 255, byte(n * 20), 11, 20, 255}; !reflect.DeepEqual(got, want) {
			t.Errorf("image %d: %v, want %v", n, got, want)
		}
	}
	big, err := g.NewImage(1024, 1024) // 4 MiB: an allocation of its own
	if err != nil {
		t.Fatal(err)
	}
	if big.(*Image).mem.block != nil {
		t.Error("an image larger than half a block was put in a block")
	}
	big.Dispose()
	for _, img := range imgs {
		img.Dispose()
	}
	if err := g.flush(); err != nil {
		t.Fatal(err)
	}
	g.device.WaitIdle()
	g.retireUpTo(g.serial, true)
	g.runAfterBatch()
	blocks = 0
	for _, bs := range g.memBlocks {
		blocks += len(bs)
	}
	if blocks != 1 {
		t.Errorf("after disposing every image, %d blocks kept (want 1)", blocks)
	}
}
