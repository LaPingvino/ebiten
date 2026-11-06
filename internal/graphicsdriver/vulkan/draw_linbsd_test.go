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
func preserved(dw, dh int, src0 image.Rectangle, sw, sh int) []uint32 {
	f := func(v float32) uint32 { return math.Float32bits(v) }
	u := make([]uint32, graphics.PreservedUniformDwordCount)
	u[0], u[1] = f(float32(dw)), f(float32(dh))
	u[2], u[3] = f(float32(sw)), f(float32(sh))
	u[12], u[13] = f(float32(dw)), f(float32(dh))
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
