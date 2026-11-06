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
	"bytes"
	"errors"
	"image"
	"strings"
	"testing"
	"time"

	"github.com/hajimehoshi/ebiten/v2/internal/color"
	"github.com/hajimehoshi/ebiten/v2/internal/graphicsdriver"
	"github.com/hajimehoshi/ebiten/v2/internal/graphicsdriver/vulkan/vk"
)

// testGraphics is an initialized driver with the validation layer on (its
// errors fail the test), or a skip where there is no Vulkan 1.3.
func testGraphics(t *testing.T) *Graphics {
	t.Helper()
	debugCallback = func(m vk.DebugMessage) {
		if m.Severity&vk.DebugUtilsMessageSeverityErrorBitEXT != 0 {
			t.Errorf("validation: %s: %s", m.ID, m.Text)
		} else {
			t.Logf("validation warning: %s: %s", m.ID, m.Text)
		}
	}
	t.Cleanup(func() { debugCallback = nil })
	gd, err := NewGraphics(color.ColorSpaceSRGB)
	if err != nil {
		t.Fatal(err)
	}
	g := gd.(*Graphics)
	if err := g.Initialize(); err != nil {
		if strings.Contains(err.Error(), "no Vulkan") || strings.Contains(err.Error(), "incompatible driver") {
			t.Skip(err)
		}
		t.Fatal(err)
	}
	t.Cleanup(g.destroy)
	if g.messenger == 0 {
		t.Log("no validation layer installed: the test runs without it")
	}
	return g
}

func pixels(w, h int, f func(x, y int) [4]byte) []byte {
	p := make([]byte, 4*w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := f(x, y)
			copy(p[4*(y*w+x):], c[:])
		}
	}
	return p
}

func read(t *testing.T, img graphicsdriver.Image, r image.Rectangle) []byte {
	t.Helper()
	out := make([]byte, 4*r.Dx()*r.Dy())
	rb, err := img.ReadPixelsAsync([]graphicsdriver.PixelsArgs{{Pixels: out, Region: r}})
	if err != nil {
		t.Fatal(err)
	}
	defer rb.Dispose()
	if err := rb.Wait(); err != nil {
		t.Fatal(err)
	}
	return out
}

// Pixels written to an image read back the same — the whole of it, a
// region, several regions in one call; a new image's unwritten pixels are
// transparent black.
func TestWriteReadPixels(t *testing.T) {
	g := testGraphics(t)
	const w, h = 5, 4
	img, err := g.NewImage(w, h)
	if err != nil {
		t.Fatal(err)
	}
	defer img.Dispose()
	all := pixels(w, h, func(x, y int) [4]byte { return [4]byte{byte(x * 50), byte(y * 60), 7, 255} })
	if err := img.WritePixels([]graphicsdriver.PixelsArgs{{Pixels: all, Region: image.Rect(0, 0, w, h)}}); err != nil {
		t.Fatal(err)
	}
	if got := read(t, img, image.Rect(0, 0, w, h)); !bytes.Equal(got, all) {
		t.Errorf("whole image: %v, want %v", got, all)
	}
	sub := read(t, img, image.Rect(1, 2, 3, 4))
	want := append(append([]byte{}, all[4*(2*w+1):4*(2*w+3)]...), all[4*(3*w+1):4*(3*w+3)]...)
	if !bytes.Equal(sub, want) {
		t.Errorf("region: %v, want %v", sub, want)
	}

	img2, err := g.NewImage(w, h)
	if err != nil {
		t.Fatal(err)
	}
	defer img2.Dispose()
	red := pixels(2, 1, func(int, int) [4]byte { return [4]byte{255, 0, 0, 255} })
	blue := pixels(1, 2, func(int, int) [4]byte { return [4]byte{0, 0, 255, 255} })
	if err := img2.WritePixels([]graphicsdriver.PixelsArgs{
		{Pixels: red, Region: image.Rect(0, 0, 2, 1)},
		{Pixels: blue, Region: image.Rect(4, 2, 5, 4)},
	}); err != nil {
		t.Fatal(err)
	}
	got := read(t, img2, image.Rect(0, 0, w, h))
	at := func(x, y int) [4]byte { var c [4]byte; copy(c[:], got[4*(y*w+x):]); return c }
	for _, c := range []struct {
		x, y int
		want [4]byte
	}{{0, 0, [4]byte{255, 0, 0, 255}}, {1, 0, [4]byte{255, 0, 0, 255}}, {2, 0, [4]byte{}}, {4, 2, [4]byte{0, 0, 255, 255}}, {4, 3, [4]byte{0, 0, 255, 255}}, {0, 3, [4]byte{}}} {
		if at(c.x, c.y) != c.want {
			t.Errorf("two regions: pixel (%d, %d) = %v, want %v", c.x, c.y, at(c.x, c.y), c.want)
		}
	}

	img3, err := g.NewImage(2, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer img3.Dispose()
	if got := read(t, img3, image.Rect(0, 0, 2, 2)); !bytes.Equal(got, make([]byte, 16)) {
		t.Errorf("never written: %v, want transparent black", got)
	}
}

// A read-back sees the writes recorded before it and not those after it;
// it is not done until its batch is submitted (End), and Poll — which
// never waits — reports it once the device has finished that batch.
func TestReadPixelsAsyncOrder(t *testing.T) {
	g := testGraphics(t)
	img, err := g.NewImage(2, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer img.Dispose()
	a := []byte{1, 2, 3, 4, 5, 6, 7, 8}
	b := []byte{9, 9, 9, 9, 9, 9, 9, 9}
	if err := g.Begin(); err != nil {
		t.Fatal(err)
	}
	if err := img.WritePixels([]graphicsdriver.PixelsArgs{{Pixels: a, Region: image.Rect(0, 0, 2, 1)}}); err != nil {
		t.Fatal(err)
	}
	out := make([]byte, 8)
	rb, err := img.ReadPixelsAsync([]graphicsdriver.PixelsArgs{{Pixels: out, Region: image.Rect(0, 0, 2, 1)}})
	if err != nil {
		t.Fatal(err)
	}
	defer rb.Dispose()
	if err := img.WritePixels([]graphicsdriver.PixelsArgs{{Pixels: b, Region: image.Rect(0, 0, 2, 1)}}); err != nil {
		t.Fatal(err)
	}
	if done, err := rb.Poll(); err != nil || done {
		t.Fatalf("Poll before End: %v, %v; want not done", done, err)
	}
	if err := g.End(graphicsdriver.FlushModeIntermediate); err != nil {
		t.Fatal(err)
	}
	// End submits without waiting: Poll (never blocking) reports the
	// read-back done once the device has finished its batch.
	deadline := time.Now().Add(5 * time.Second)
	for {
		done, err := rb.Poll()
		if err != nil {
			t.Fatal(err)
		}
		if done {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Poll after End: never done")
		}
		time.Sleep(time.Millisecond)
	}
	if !bytes.Equal(out, a) {
		t.Errorf("read %v, want the first write %v", out, a)
	}
	if got := read(t, img, image.Rect(0, 0, 2, 1)); !bytes.Equal(got, b) {
		t.Errorf("then %v, want the second write %v", got, b)
	}
}

// A lost device is reported, explained and still matchable (errors.Is); the
// window-system results print by name.
func TestDeviceError(t *testing.T) {
	err := deviceError(vk.ErrorDeviceLost)
	if !errors.Is(err, vk.ErrorDeviceLost) || !strings.Contains(err.Error(), "device was lost") {
		t.Errorf("device lost: %v", err)
	}
	if err := deviceError(vk.ErrorOutOfDeviceMemory); !errors.Is(err, vk.ErrorOutOfDeviceMemory) || strings.Contains(err.Error(), "lost") {
		t.Errorf("out of memory: %v", err)
	}
	for _, r := range []vk.Result{vk.NotReady, vk.Timeout, vk.SuboptimalKHR, vk.ErrorOutOfDateKHR, vk.ErrorSurfaceLostKHR} {
		if strings.Contains(r.Error(), "VkResult") {
			t.Errorf("%d has no name", int32(r))
		}
	}
}
