// Copyright 2026 The Ebitengine Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
// Command frametime times frames on the current graphics driver (LOOP V8):
// a scene drawn with vsync off, a warm-up, then N frames, the mean and the
// 95th percentile in ms per frame on one line. Run it once per driver
// (EBITENGINE_GRAPHICS_LIBRARY=opengl / vulkan; frametime.sh does both).
//
//	go run ./internal/graphicsdriver/vulkan/frametime -scene sprites -frames 600
package main

import (
	"flag"
	"fmt"
	"image/color"
	"math"
	"math/rand/v2"
	"os"
	"sort"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
)

var (
	sceneName = flag.String("scene", "sprites", "sprites (many small images: batching) or shader (a full-screen Kage shader)")
	frames    = flag.Int("frames", 600, "frames timed after the warm-up")
	warmup    = flag.Int("warmup", 60, "frames drawn before timing")
	count     = flag.Int("n", 5000, "sprites drawn per frame (scene sprites)")
)

const w, h = 1280, 720

// a full-screen pattern with a little arithmetic per pixel
const kage = `//kage:unit pixels
package main

var Time float

func Fragment(dst vec4, src vec2, c vec4) vec4 {
	p := dst.xy / vec2(1280, 720)
	v := sin(p.x*20+Time) + sin(p.y*14-Time) + sin((p.x+p.y)*9+Time*0.5)
	return vec4(0.5+0.5*sin(v), 0.5+0.5*sin(v+2), 0.5+0.5*sin(v+4), 1)
}
`

type sprite struct{ x, y, vx, vy float64 }

type game struct {
	sprite  *ebiten.Image
	shader  *ebiten.Shader
	sprites []sprite
	n       int
	last    time.Time
	times   []time.Duration
	t       float32
}

func (g *game) Update() error {
	for i := range g.sprites {
		s := &g.sprites[i]
		s.x, s.y = s.x+s.vx, s.y+s.vy
		if s.x < 0 || s.x > w-16 {
			s.vx = -s.vx
		}
		if s.y < 0 || s.y > h-16 {
			s.vy = -s.vy
		}
	}
	g.t += 1.0 / 60
	return nil
}

func (g *game) Draw(screen *ebiten.Image) {
	now := time.Now()
	if g.n > *warmup && !g.last.IsZero() {
		g.times = append(g.times, now.Sub(g.last))
	}
	g.last = now
	g.n++
	switch *sceneName {
	case "shader":
		op := &ebiten.DrawRectShaderOptions{Uniforms: map[string]any{"Time": g.t}}
		screen.DrawRectShader(w, h, g.shader, op)
	default:
		screen.Fill(color.RGBA{0x20, 0x24, 0x40, 0xff})
		var op ebiten.DrawImageOptions
		for _, s := range g.sprites {
			op.GeoM.Reset()
			op.GeoM.Translate(s.x, s.y)
			screen.DrawImage(g.sprite, &op)
		}
	}
	if len(g.times) >= *frames {
		report(g.times)
		os.Exit(0)
	}
}

func (g *game) Layout(int, int) (int, int) { return w, h }

func report(ts []time.Duration) {
	ms := make([]float64, len(ts))
	var sum float64
	for i, t := range ts {
		ms[i] = float64(t.Microseconds()) / 1000
		sum += ms[i]
	}
	sort.Float64s(ms)
	p95 := ms[int(math.Ceil(0.95*float64(len(ms))))-1]
	lib := os.Getenv("EBITENGINE_GRAPHICS_LIBRARY")
	if lib == "" {
		lib = "auto"
	}
	fmt.Printf("%-7s %-8s %5d frames  mean %7.3f ms  p95 %7.3f ms  (%.0f fps)\n", lib, *sceneName, len(ms), sum/float64(len(ms)), p95, 1000*float64(len(ms))/sum)
}

func main() {
	flag.Parse()
	g := &game{sprite: ebiten.NewImage(16, 16)}
	g.sprite.Fill(color.RGBA{0xe0, 0xb0, 0x40, 0xff})
	r := rand.New(rand.NewPCG(1, 2)) // the same sprites on every driver
	for range *count {
		g.sprites = append(g.sprites, sprite{r.Float64() * (w - 16), r.Float64() * (h - 16), r.Float64()*4 - 2, r.Float64()*4 - 2})
	}
	if *sceneName == "shader" {
		s, err := ebiten.NewShader([]byte(kage))
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		g.shader = s
	}
	ebiten.SetWindowSize(w, h)
	ebiten.SetWindowTitle("frametime")
	ebiten.SetVsyncEnabled(false)
	ebiten.SetTPS(ebiten.SyncWithFPS)
	if err := ebiten.RunGame(g); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
