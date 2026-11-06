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
	"image"
	"math"
	"testing"

	"github.com/hajimehoshi/ebiten/v2/internal/builtinshader"
	"github.com/hajimehoshi/ebiten/v2/internal/graphics"
	"github.com/hajimehoshi/ebiten/v2/internal/graphicsdriver"
)

// preserved is Ebitengine's preserved uniforms as graphicscommand's
// prependPreservedUniforms makes them: the sizes, the regions, the
// projection.
// The sizes are the images' internal ones (graphics.InternalImageSize), the
// destination region the logical one.
func preserved(w, h int, src0 image.Rectangle, sw, sh int) []uint32 {
	f := func(v float32) uint32 { return math.Float32bits(v) }
	u := make([]uint32, graphics.PreservedUniformDwordCount)
	dw, dh := graphics.InternalImageSize(w), graphics.InternalImageSize(h)
	u[0], u[1] = f(float32(dw)), f(float32(dh))
	if sw > 0 {
		u[2], u[3] = f(float32(graphics.InternalImageSize(sw))), f(float32(graphics.InternalImageSize(sh)))
	}
	u[12], u[13] = f(float32(w)), f(float32(h))
	u[14], u[15] = f(float32(src0.Min.X)), f(float32(src0.Min.Y))
	u[22], u[23] = f(float32(src0.Dx())), f(float32(src0.Dy()))
	for i, v := range []float32{2 / float32(dw), 0, 0, 0, 0, 2 / float32(dh), 0, 0, 0, 0, 1, 0, -1, -1, 0, 1} {
		u[30+i] = f(v)
	}
	return u
}

// quad is a rectangle's two triangles: destination pixels (x0, y0)–(x1, y1),
// source texels the same, white.
func quad(x0, y0, x1, y1 float32) ([]float32, []uint32) {
	v := func(x, y float32) []float32 { return []float32{x, y, x, y, 1, 1, 1, 1, 0, 0, 0, 0} }
	var vs []float32
	for _, p := range [][2]float32{{x0, y0}, {x1, y0}, {x0, y1}, {x1, y1}} {
		vs = append(vs, v(p[0], p[1])...)
	}
	return vs, []uint32{0, 1, 2, 1, 2, 3}
}

// Ebitengine's own copy shader (builtinshader, nearest) draws a source
// image onto another, pixel for pixel: the vertex layout, the sampled
// source, the preserved uniforms and the projection all as Ebitengine sends
// them.
func TestDrawTrianglesCopy(t *testing.T) {
	g := testGraphics(t)
	const w, h = 4, 3
	src, _ := g.NewImage(w, h)
	dst, _ := g.NewImage(w, h)
	pattern := pixels(w, h, func(x, y int) [4]byte { return [4]byte{byte(60 * x), byte(80 * y), 200, 255} })
	if err := src.WritePixels([]graphicsdriver.PixelsArgs{{Pixels: pattern, Region: image.Rect(0, 0, w, h)}}); err != nil {
		t.Fatal(err)
	}
	p, err := graphics.CompileShader(builtinshader.ShaderSource(builtinshader.FilterNearest, builtinshader.AddressUnsafe))
	if err != nil {
		t.Fatal(err)
	}
	s, err := g.NewShader(p)
	if err != nil {
		t.Fatal(err)
	}
	vs, is := quad(0, 0, w, h)
	if err := g.SetVertices(vs, is); err != nil {
		t.Fatal(err)
	}
	var srcs [graphics.ShaderSrcImageCount]graphicsdriver.ImageID
	srcs[0] = src.ID()
	if err := g.DrawTriangles(dst.ID(), srcs, s.ID(), []graphicsdriver.DstRegion{{Region: image.Rect(0, 0, w, h), IndexCount: 6}}, 0,
		graphicsdriver.BlendCopy, preserved(w, h, image.Rect(0, 0, w, h), w, h)); err != nil {
		t.Fatal(err)
	}
	if err := g.End(graphicsdriver.FlushModeIntermediate); err != nil {
		t.Fatal(err)
	}
	if got := read(t, dst, image.Rect(0, 0, w, h)); !bytes.Equal(got, pattern) {
		t.Errorf("copied %v\nwant   %v", got, pattern)
	}
}

// A Kage fill of premultiplied half-transparent red, source-over onto
// opaque blue, through a destination region covering the left half only
// (the scissor): the left half blends to (128, 0, 128, 255), the right
// half stays blue.
func TestDrawTrianglesBlendRegion(t *testing.T) {
	g := testGraphics(t)
	const w, h = 4, 2
	dst, _ := g.NewImage(w, h)
	blue := pixels(w, h, func(int, int) [4]byte { return [4]byte{0, 0, 255, 255} })
	if err := dst.WritePixels([]graphicsdriver.PixelsArgs{{Pixels: blue, Region: image.Rect(0, 0, w, h)}}); err != nil {
		t.Fatal(err)
	}
	p, err := graphics.CompileShader([]byte(`//kage:unit pixels
package main

func Fragment(dst vec4, src vec2, color vec4) vec4 {
	return vec4(0.5, 0, 0, 0.5)
}
`))
	if err != nil {
		t.Fatal(err)
	}
	s, err := g.NewShader(p)
	if err != nil {
		t.Fatal(err)
	}
	vs, is := quad(0, 0, w, h)
	if err := g.SetVertices(vs, is); err != nil {
		t.Fatal(err)
	}
	if err := g.DrawTriangles(dst.ID(), [graphics.ShaderSrcImageCount]graphicsdriver.ImageID{}, s.ID(),
		[]graphicsdriver.DstRegion{{Region: image.Rect(0, 0, w/2, h), IndexCount: 6}}, 0,
		graphicsdriver.BlendSourceOver, preserved(w, h, image.Rectangle{}, 0, 0)); err != nil {
		t.Fatal(err)
	}
	if err := g.End(graphicsdriver.FlushModeIntermediate); err != nil {
		t.Fatal(err)
	}
	got := read(t, dst, image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			want := [4]int{0, 0, 255, 255}
			if x < w/2 {
				want = [4]int{128, 0, 128, 255}
			}
			c := got[4*(y*w+x):]
			for k := range want {
				if d := int(c[k]) - want[k]; d < -1 || d > 1 {
					t.Errorf("pixel (%d, %d) = %v, want ≈ %v", x, y, c[:4], want)
					break
				}
			}
		}
	}
}

// Ebitengine's sequence in TestImageWritePixelsBetweenDraws, call for call:
// both images cleared by draws, then the source written, drawn source-over
// onto the left of the destination, written again and drawn onto its right
// — one batch, one vertex buffer; the read sees both draws, each with the
// pixels written just before it.
func TestWritePixelsBetweenDraws(t *testing.T) {
	g := testGraphics(t)
	p, err := graphics.CompileShader(builtinshader.ShaderSource(builtinshader.FilterNearest, builtinshader.AddressUnsafe))
	if err != nil {
		t.Fatal(err)
	}
	s, err := g.NewShader(p)
	if err != nil {
		t.Fatal(err)
	}
	src, _ := g.NewImage(2, 1)
	dst, _ := g.NewImage(4, 1)
	v := func(x, y, sx, sy, c float32) []float32 { return []float32{x, y, sx, sy, c, c, c, c, 0, 0, 0, 0} }
	var vs []float32
	for _, q := range [][5]float32{{2, 1, 0, 0, 0}, {4, 1, 0, 0, 0}, {2, 1, 0, 0, 1}, {2, 1, 2, 0, 1}} { // w, h, dst x, -, colour
		x0 := q[2]
		sw := float32(2)
		if q[4] == 0 {
			x0, sw = 0, 0
		}
		vs = append(vs, v(x0, 0, 0, 0, q[4])...)
		vs = append(vs, v(x0+q[0], 0, sw, 0, q[4])...)
		vs = append(vs, v(x0, q[1], 0, q[1]*sw/2, q[4])...)
		vs = append(vs, v(x0+q[0], q[1], sw, q[1]*sw/2, q[4])...)
	}
	var is []uint32
	for k := uint32(0); k < 4; k++ {
		is = append(is, 4*k, 4*k+1, 4*k+2, 4*k+1, 4*k+2, 4*k+3)
	}
	if err := g.SetVertices(vs, is); err != nil {
		t.Fatal(err)
	}
	none := [graphics.ShaderSrcImageCount]graphicsdriver.ImageID{}
	withSrc := none
	withSrc[0] = src.ID()
	draw := func(d graphicsdriver.Image, srcs [graphics.ShaderSrcImageCount]graphicsdriver.ImageID, w, h, off int, blend graphicsdriver.Blend) {
		t.Helper()
		if err := g.DrawTriangles(d.ID(), srcs, s.ID(), []graphicsdriver.DstRegion{{Region: image.Rect(0, 0, w, h), IndexCount: 6}}, off, blend, preserved(w, h, image.Rect(0, 0, 2, 1), 2, 1)); err != nil {
			t.Fatal(err)
		}
	}
	draw(src, none, 2, 1, 0, graphicsdriver.BlendClear)
	draw(dst, none, 4, 1, 6, graphicsdriver.BlendClear)
	red := []byte{255, 0, 0, 255, 255, 0, 0, 255}
	green := []byte{0, 255, 0, 255, 0, 255, 0, 255}
	if err := src.WritePixels([]graphicsdriver.PixelsArgs{{Pixels: red, Region: image.Rect(0, 0, 2, 1)}}); err != nil {
		t.Fatal(err)
	}
	draw(dst, withSrc, 4, 1, 12, graphicsdriver.BlendSourceOver)
	if err := src.WritePixels([]graphicsdriver.PixelsArgs{{Pixels: green, Region: image.Rect(0, 0, 2, 1)}}); err != nil {
		t.Fatal(err)
	}
	draw(dst, withSrc, 4, 1, 18, graphicsdriver.BlendSourceOver)
	if got, want := read(t, dst, image.Rect(0, 0, 4, 1)), append(append([]byte{}, red...), green...); !bytes.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// BenchmarkDrawBatch: one batch as a game frame makes it — SetVertices, 200
// draws of Ebitengine's copy shader (each its own uniforms), End.
func BenchmarkDrawBatch(b *testing.B) {
	gd, err := NewGraphics(0)
	if err != nil {
		b.Fatal(err)
	}
	g := gd.(*Graphics)
	if err := g.Initialize(); err != nil {
		b.Skip(err)
	}
	defer g.destroy()
	p, err := graphics.CompileShader(builtinshader.ShaderSource(builtinshader.FilterNearest, builtinshader.AddressUnsafe))
	if err != nil {
		b.Fatal(err)
	}
	s, _ := g.NewShader(p)
	src, _ := g.NewImage(16, 16)
	dst, _ := g.NewImage(256, 256)
	_ = src.WritePixels([]graphicsdriver.PixelsArgs{{Pixels: make([]byte, 16*16*4), Region: image.Rect(0, 0, 16, 16)}})
	const draws = 200
	var vs []float32
	var is []uint32
	for k := 0; k < draws; k++ {
		x := float32(k % 16 * 16)
		y := float32(k / 16 * 16)
		q, qi := quad(x, y, x+16, y+16)
		vs = append(vs, q...)
		for _, i := range qi {
			is = append(is, i+uint32(4*k))
		}
	}
	var srcs [graphics.ShaderSrcImageCount]graphicsdriver.ImageID
	srcs[0] = src.ID()
	u := preserved(256, 256, image.Rect(0, 0, 16, 16), 16, 16)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := g.SetVertices(vs, is); err != nil {
			b.Fatal(err)
		}
		for k := 0; k < draws; k++ {
			if err := g.DrawTriangles(dst.ID(), srcs, s.ID(), []graphicsdriver.DstRegion{{Region: image.Rect(0, 0, 256, 256), IndexCount: 6}}, 6*k, graphicsdriver.BlendSourceOver, u); err != nil {
				b.Fatal(err)
			}
		}
		if err := g.End(graphicsdriver.FlushModeIntermediate); err != nil {
			b.Fatal(err)
		}
	}
}
