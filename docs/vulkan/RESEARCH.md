# A Vulkan driver for Ebitengine — research

Notes for the design (DESIGN.md). Read from Ebitengine's main branch (c487dc97, November 2026). Facts about
Ebitengine are cited by file; anything about Vulkan itself is from the Vulkan 1.3 specification.

## 1. The driver contract (internal/graphicsdriver)

A driver implements three interfaces; everything above them (internal/graphicscommand, internal/atlas, the
Kage compiler) is shared by all drivers and stays untouched.

**`Graphics`** (graphics.go):

| Method | What it promises |
|---|---|
| `Initialize()` | create the device; called once on the render thread |
| `ColorSpace()` | the screen's colour space (sRGB on every current driver) |
| `Begin()` / `End(FlushMode)` | bracket a command batch. `FlushModeIntermediate` submits without finishing the frame, `FlushModeEndFrame` finishes it without presenting, `FlushModePresent` finishes and presents — so `End` is where a Vulkan driver submits, and with `Present` also queues the swapchain image |
| `SetTransparent(bool)` | a transparent window (the swapchain's composite alpha) |
| `SetVertices(vertices []float32, indices []uint32)` | the batch's vertex and index data, uploaded once, then drawn from by offset |
| `NewImage(w, h)` / `NewScreenFramebufferImage(w, h)` | an offscreen image, and the screen's (the swapchain image of the frame) |
| `SetVsyncEnabled(bool)` | FIFO vs. MAILBOX / IMMEDIATE present mode |
| `NeedsClearingScreen()` | whether the screen must be cleared each frame (true where the swapchain image's content is undefined — Vulkan's case with `LOAD_OP_DONT_CARE`) |
| `MaxImageSize()` | the device's `maxImageDimension2D` |
| `NewShader(*shaderir.Program)` | compile a Kage program (already Ebitengine's IR) for this driver |
| `DrawTriangles(dst, srcs[ShaderSrcImageCount], shader, dstRegions, indexOffset, blend, uniforms)` | one draw call per destination region (a scissor each), with up to `ShaderSrcImageCount` source images, the blend state, and the uniforms as packed `uint32`s |

`Resetter` (optional): `Reset()` after a device loss — Vulkan's `VK_ERROR_DEVICE_LOST` maps to it.

**`Image`**: `ID`, `Dispose`, `WritePixels([]PixelsArgs)` (regions of RGBA bytes), and
`ReadPixelsAsync([]PixelsArgs) (PixelReadback, error)`, which **must not wait for the GPU**: it records a copy
at the current point of the command stream; `PixelReadback.Poll()` reports completion without waiting,
`Wait()` blocks (outside a batch), `Dispose()` exactly once. For Vulkan: a buffer copy in the batch's command
buffer, a fence (or a timeline semaphore value) to poll, mapped host-visible memory to copy out of.

**`Shader`**: `ID`, `Dispose`.

**Blend** (blend.go): separate RGB / alpha factors (10 factors) and operations (add, subtract, reverse
subtract, min, max) — a one-to-one fit with `VkPipelineColorBlendAttachmentState`, so each blend is part of
the pipeline key.

## 2. How a driver is chosen (internal/ui)

`internal/ui/graphics.go` reads `EBITENGINE_GRAPHICS_LIBRARY` (`opengl`, `directx`, `metal`, `playstation5`;
anything else is an error) and calls the platform's `graphicsDriverCreator` (`newAuto`, `newOpenGL`,
`newDirectX`, …). On Linux / BSD (`ui_linbsd.go`) `newAuto` is OpenGL, the others return "not supported". A
Vulkan driver needs: a `GraphicsLibraryVulkan` constant, `"vulkan"` in that switch, a `newVulkan` in the
creator interface (each platform's creator gets one; all but Linux return "not supported"), and — later —
whether `newAuto` should try Vulkan first. The public `ebiten.GraphicsLibrary` enum mirrors this.

## 3. The window on Linux (internal/glfw)

Ebitengine's GLFW on Linux is its **own pure-Go port** (`internal/glfw`, purego, no cgo), **X11 only**
(`x11_platform_linbsd.go`; on Wayland it runs through XWayland — `ui_linbsd.go` already notes the EGL vs. GLX
choice for that case). It exports `GetX11Display()` and `(*Window).GetX11Window()` (`native_x11_linbsd.go`):
exactly what `VK_KHR_xlib_surface`'s `vkCreateXlibSurfaceKHR` takes. So the swapchain needs no change to the
window layer, and no native Wayland surface at first (XWayland covers it, as it does for OpenGL today).

## 4. Sizes, for scale

The OpenGL driver with its bindings: ~6,200 lines. Metal's: ~5,000 (its `mtl` / `ca` / `ns` bindings copied
from Dmitri Shuralyov's packages, with permission — metal/README.md). The MSL shader backend: 745 lines.

## 5. Testing here

- **No Vulkan device on this VPS yet**: the loader (vulkan-icd-loader) is installed, but the only driver is
  radeon's ICD (no such GPU). Lavapipe — Mesa's software Vulkan — comes in Arch's `vulkan-swrast` package;
  installing it needs sudo (asked).
- **SPIR-V tools are here**: `spirv-val` and `spirv-dis` (spirv-tools), and `glslangValidator` — test-time
  checks for the SPIR-V backend, never run-time dependencies.

## 6. The shader IR (internal/shaderir) and what SPIR-V needs from it

Kage is compiled once, by Ebitengine, into `shaderir.Program` (program.go); each driver turns that into its
own language. The backends that exist emit text: `glsl` (OpenGL, WebGL), `hlsl` (DirectX), `msl` (Metal — 587
+ 158 lines, the closest model), `pssl` (PlayStation 5). A SPIR-V backend emits a binary module instead, which
changes the shape of the work but not its inputs:

- **The program**: uniform names and types (packed by the driver into `[]uint32` — how each driver lays them
  out still to read), `TextureCount` source images, vertex attributes, varyings, helper functions, and one
  vertex and one fragment entry point.
- **Types** (type.go): bool, int, float, vec2–4, ivec2–4, mat2–4, texture, arrays (with a length), structs.
  All map directly onto SPIR-V types (`OpTypeBool`, `OpTypeInt 32 1`, `OpTypeFloat 32`, `OpTypeVector`,
  `OpTypeMatrix`, `OpTypeArray`, `OpTypeStruct`); textures become a `sampler2D`-like combined image sampler or
  a separate image + sampler pair.
- **Statements**: expression, block, assign, init, if, for, continue, break, return, discard. SPIR-V wants
  *structured* control flow: every `if` needs an `OpSelectionMerge` block, every `for` an `OpLoopMerge` with a
  continue target — Kage's `for` has a fixed form (`ForVarType`, `ForInit`, `ForEnd`, `ForOp`, `ForDelta`), so
  each loop maps to one canonical header / body / continue / merge shape. Local variables can stay
  `OpVariable`s of `Function` storage with loads and stores — no SSA construction needed; drivers optimise.
  `discard` is `OpKill` (or `OpTerminateInvocation`, SPIR-V 1.6).
- **Expressions**: number literals, uniform / texture / local variables, struct members, swizzles, unary and
  binary operators (arithmetic, component-wise vs. matrix multiply, shifts, comparisons, vector equality,
  bit ops, `&&` / `||` — the short-circuit ones need control flow in SPIR-V, or `OpLogicalAnd` / `Or` where
  both sides are side-effect free), selection (`?:`), calls, field selectors, indexing.
- **Built-in functions** (56): constructors (`bool` … `mat4`) → `OpCompositeConstruct` / conversions; most of
  the maths (`sin` … `refract`, `mix`, `smoothstep`, `clamp`, …) → the `GLSL.std.450` extended instruction set,
  one instruction each; `dot` / `transpose` → core ops; `dfdx` / `dfdy` / `fwidth` → `OpDPdx` / `OpDPdy` /
  `OpFwidth`; `mod` → `OpFMod` (GLSL's sign rule, not `OpFRem`); `atan2` → `Atan2`; `__texelAt` (the texture
  read every Kage `imageSrcNAt` turns into) → an image sample at explicit LOD 0 with Ebitengine's nearest
  filtering; `frontfacing` → the `FrontFacing` built-in; `len` / `cap` → constants (arrays are fixed size).

So the emitter is: a type / constant table with de-duplication, a function writer with structured blocks, and
an expression lowering — sized like the MSL backend plus a binary writer. `spirv-val` checks every module it
writes in the tests; `spirv-dis` makes the tests' expected output readable.

## Still to read (next ticks)

- How each driver packs the uniforms into `[]uint32` (std140-like alignment? per-driver?), and the vertex
  layout (Ebitengine's fixed vertex format: position, texture coords, colour, custom attributes).
- How the opengl and metal drivers organise a frame: vertex / index buffers, uniform layout per shader, image
  ↔ texture handling, the screen image, read-backs.
- Ebitengine's own discussions of Vulkan (issues on hajimehoshi/ebiten) and the author's stance.
- purego's limits for Vulkan (struct-heavy C API: passing pointers to Go-allocated structs, callbacks).
