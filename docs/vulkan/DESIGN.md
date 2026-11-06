# A Vulkan driver for Ebitengine — design

Status: built (November 2026) — offscreen drawing, Kage on SPIR-V and the screen are in and tested; what is
still open is listed at the end. Built on [RESEARCH.md](RESEARCH.md); section references (§n) point there.
How to try it with a game: [TRY.md](TRY.md).

## Goal and scope

A `vulkan` graphics driver beside opengl / directx / metal / playstation5, chosen with
`EBITENGINE_GRAPHICS_LIBRARY=vulkan` or `ebiten.GraphicsLibraryVulkan` (not by `newAuto` yet). The target is
**Linux and the BSDs on amd64 and arm64, X11** — Wayland desktops included, through XWayland: Ebitengine's
window on Linux is always an X11 one (its glfw has no Wayland backend), so the Xlib surface covers both
(§3). Out of scope, each a follow-up of its own: Windows (DirectX is there), macOS / iOS (Metal; MoltenVK
would add nothing), Android (Vulkan is attractive there — §8 — but Ebitengine's mobile layer is a project in
itself), 32-bit targets.

Nothing above the driver changes: `internal/graphicscommand`, `internal/atlas` and the Kage compiler are
shared and stay as they are. Outside `internal/graphicsdriver/vulkan` and `internal/shaderir/spirv` the edits
are the ones a new driver needs — `ui.GraphicsLibraryVulkan` (appended, the other values unchanged) and the
public `ebiten.GraphicsLibraryVulkan`, `"vulkan"` in the environment switch, a `newVulkan` on each platform's
driver creator (all but Linux / BSD returning "not supported") — and one more: `internal/ui`'s
`nativeWindow` on Linux / BSD, a TODO that returned 0, now returns the X11 window (OpenGL takes its window
through `SetPresenter` first; Linux gamepads don't use it).

## Layout

| package | what |
|---|---|
| `internal/graphicsdriver/vulkan/vk` | the Vulkan bindings: the loader opened with purego's `Dlopen`, every other function through `vkGetInstanceProcAddr` / `vkGetDeviceProcAddr` (the swapchain's loaded on first use — they exist only on a device made with `VK_KHR_swapchain`); Go structs laid out as the C ones; only what the driver uses (§9) |
| `internal/graphicsdriver/vulkan` | the driver: `Graphics`, `Image`, `Shader`, read-backs, presenting (§1) |
| `internal/shaderir/spirv` | Kage's IR → SPIR-V modules (§6); no Vulkan in it, testable on its own |

## Decisions

**Vulkan 1.3 as the baseline.** Dynamic rendering (no render-pass or framebuffer objects) and
synchronization2 are core there, which keeps the driver much smaller; lavapipe, Mesa's hardware drivers and
Crostini's Venus all offer 1.3. A device without 1.3 makes `Initialize` fail.

**Frames in flight: three.** A ring of batches, each a command buffer, a fence, a serial and what to release
once the device is done with it. `End` submits with the batch's fence and returns at once; recording into a
ring slot first waits for the batch last in it. Anything freed while the device may still use it — images,
shaders, staging and read-back buffers, descriptor pools — goes through `release`: after the batch being
recorded, else after the newest one in flight, else at once. Teardown waits for the device first (a
`BenchmarkDrawBatch` run segfaulted when it didn't: the validation layer named it).

**The ring.** Each batch slot has one host-visible, coherent buffer (1 MiB to start, doubled when full, the
old one freed after the batch) that its vertices, indices and uniforms are suballocated from, emptied when the
slot is recorded anew. Uniform blocks sit at 256-byte offsets (`minUniformBufferOffsetAlignment`'s maximum).
Measured on lavapipe, against a new buffer per call: a 200-draw batch 21 → 13 ms, 10 657 → 7 217
allocations.

**Draws.** `SetVertices` copies the batch's vertices and indices into the ring; `DrawTriangles` renders into
its destination with the contents kept (`VK_ATTACHMENT_LOAD_OP_LOAD`), binds them, and issues one indexed
draw per destination region, each with its scissor. Each `DrawTriangles` is its own `vkCmdBeginRendering` …
`vkCmdEndRendering`; merging a run of draws to one destination is an optimisation not made yet.

**Pipelines** are cached per shader by blend (the destination is always RGBA8): the blend maps one to one onto
`VkPipelineColorBlendAttachmentState` (§1); the vertex input is Ebitengine's fixed 12-float format, one
binding and four attributes (§7); viewport and scissor are dynamic state; front faces are counter-clockwise.

**Uniforms** go into the ring in **std140** (`spirv.PackStd140`, repacked from Ebitengine's `[]uint32` as the
Metal and DirectX drivers repack into theirs, §7), written into the draw's descriptor at their offset. Bools
are ints in the block (read as `!= 0`); matrices and arrays of them carry `ColMajor` / `MatrixStride`.

**Y axis** (corrected 2026-11-06, found by the first compiled-shader draw): Ebitengine's projection is taken
**as it is** — no flip. Its NDC y = −1 is pixel row 0; Vulkan's NDC y points down like its framebuffer, so
NDC −1 is memory row 0, the same rows OpenGL's offscreen textures get (bottom-up storage, NDC up). Metal and
DirectX flip because their NDC points up over a top-down framebuffer; Vulkan has neither mismatch. The first
plan here — negate Y as Metal does — would have drawn every image upside down; TestCompiledVertexShader
checks both ways. `frontfacing()` agrees with OpenGL offscreen for the same reason.

**Textures** (sampler corrected 2026-11-06): each source image is a combined image sampler (set 0, bindings
1–4; a 1×1 transparent image stands in for absent ones) with **unnormalized coordinates, nearest filtering
and clamp-to-border transparent black** — Metal's `sampler(coord::pixel, filter::nearest,
address::clamp_to_zero)`: a read outside the image is transparent, which the linear-filter shaders rely on at
the edges. One descriptor set per draw, from per-batch pools of 256 sets.

**Images** are `VK_FORMAT_R8G8B8A8_UNORM`, optimally tiled, device-local, each its own allocation, at
Ebitengine's **internal size** (`graphics.InternalImageSize`: a power of two, at least 16 — the projection
Ebitengine sends is for that size; a 4×1 image drawn with a 16-wide projection read back empty). Their layout
is tracked and moved with all-commands barriers; a never-written image is cleared to transparent black first.
`WritePixels` copies through a staging buffer per call; `ReadPixelsAsync` records a `vkCmdCopyImageToBuffer`
into a host-visible buffer of its own at the current point of the stream; `Poll` checks the batch's fence
without waiting, `Wait` waits for that batch alone — the "never wait for the GPU" contract (§1).

**The screen** is an offscreen RGBA image of the window's size that Ebitengine draws into. `ui` hands the
driver the X11 window (`SetWindow`, before `Initialize`); the surface is made at the first present
(`vkCreateXlibSurfaceKHR`, §3), when the queue can present there. `End(FlushModePresent)` acquires a
swapchain image (on the batch slot's semaphore), blits the screen into it — `vkCmdBlitImage` converts RGBA to
the swapchain's BGRA and scales to its extent —, submits waiting for the acquire and signalling the image's
semaphore, and presents. The swapchain (B8G8R8A8 / R8G8B8A8 UNORM, `minImageCount + 1` images) is remade at a
new size, when suboptimal or out of date, and when vsync changes: on, FIFO; off, MAILBOX where offered, else
IMMEDIATE. Without the window-system extensions, or a window, the screen is drawn and not shown.

**Shaders**: `NewShader` hands the `shaderir.Program` to `spirv.CompileVertex` / `CompileFragment` and makes
`VkShaderModule`s of them. The SPIR-V backend writes the binary itself: a type / constant table with
de-duplication, structured control flow for Kage's `if` and fixed-form `for`, `discard` as `OpKill`, locals
as function-storage variables declared at the function's start and **zeroed when their block is entered** (a
loop body's on every iteration — Kage's declarations zero as Go's; leaving them undefined drew wrong colours
on lavapipe), the maths through `GLSL.std.450` (variadic `min` / `max` folded pairwise), short-circuit `&&` /
`||` as real control flow (§6).

**Device loss** (`VK_ERROR_DEVICE_LOST` from a submit, a fence wait or a present) is reported, explained,
and ends the game — as on the desktop OpenGL and DirectX drivers. Ebitengine's way back (`Resetter`, then
the atlas restoring every image) is built for a mobile app's pause: the images are read back *before* the
context goes. A sudden loss leaves nothing to restore from, and Ebitengine keeps no CPU copies of its images,
so recreating the device alone would bring back a blank game. (The plan here first said "recreate the device
through `Resetter`"; looking at what the atlas needs showed why not.)

**Validation** is a switch: `EBITENGINE_VULKAN_VALIDATION=1` turns on the Khronos layer for any game or test,
its messages on stderr; the driver's own tests turn it on themselves and fail on its errors.

## Testing

- `internal/shaderir/spirv`: every module its tests write passes `spirv-val`; `TestCoverage` compiles all 70
  of `internal/shader`'s test programs, library and stages, and validates them.
- `internal/graphicsdriver/vulkan/vk`: an instance and a device, images cleared, drawn and read back,
  compiled Kage drawn, a swapchain presenting into an X11 window (Xvfb, opened through libX11 with purego)
  read back with `XGetImage` — on lavapipe, under the validation layer.
- The driver: Ebitengine's own suites with `EBITENGINE_GRAPHICS_LIBRARY=vulkan` and the validation layer on
  lavapipe under Xvfb — the `ebiten` package 209 / 209 (two derivative tests skipped on every driver),
  `internal/graphicscommand`, `internal/atlas`, `internal/buffered` — with no validation message.
  `BenchmarkDrawBatch` measures a frame-like batch.
- Games: Ebitengine's `examples/2048` and `examples/shader`, goloco's title screen and a GameViber starter
  game on the branch under Xvfb — the last two pixel-identical to OpenGL.
- A real GPU: a Chromebook in Crostini (Venus) — the case that started this — still to be tried.

## Order of work

1. `vk`: the loader and an instance / device / image test on lavapipe. **Done.**
2. `spirv`: types, constants, expressions, control flow, the built-ins — validated as it grew. **Done.**
3. The driver, offscreen: images, `WritePixels`, `DrawTriangles`, read-backs — Ebitengine's tests. **Done.**
4. The screen: surface, swapchain, present, vsync, resize. **Done.**
5. Device loss: reported and explained (see Decisions — recovering needs saved images Ebitengine doesn't
   keep). Still open: merging draws to one destination into one rendering pass, a suballocator for images,
   and frame times against OpenGL on a real GPU.
