# A Vulkan driver for Ebitengine — design

Status: proposal (November 2026). Built on [RESEARCH.md](RESEARCH.md); section references (§n) point there.

## Goal and scope

A `vulkan` graphics driver beside opengl / directx / metal / playstation5, chosen with
`EBITENGINE_GRAPHICS_LIBRARY=vulkan` (and later, perhaps, by `newAuto` where Vulkan is the better path).
The first target is **Linux and the BSDs on amd64 and arm64, X11** — including Wayland desktops through
XWayland, exactly as the OpenGL driver runs today (§3). Out of scope at first, each a follow-up of its own:
native Wayland surfaces, Windows (DirectX is there), macOS / iOS (Metal; MoltenVK would add nothing), Android
(Vulkan is attractive there — §8 — but Ebitengine's mobile layer is a project in itself), 32-bit targets.

Nothing above the driver changes: `internal/graphicscommand`, `internal/atlas` and the Kage compiler are
shared and stay as they are. Outside `internal/graphicsdriver/vulkan` and `internal/shaderir/spirv`, the only
edits are the ones a new driver needs: the `GraphicsLibraryVulkan` constant, `"vulkan"` in the environment
switch, a `newVulkan` on each platform's driver creator (all but Linux / BSD returning "not supported"), and
the public `ebiten.GraphicsLibrary` value (§2).

## Layout

| package | what |
|---|---|
| `internal/graphicsdriver/vulkan/vk` | the Vulkan bindings: the loader opened with purego's `Dlopen`, every other function through `vkGetInstanceProcAddr` / `vkGetDeviceProcAddr`; Go structs laid out as the C ones; only the functions and structs the driver uses (§9) |
| `internal/graphicsdriver/vulkan` | the driver: `Graphics`, `Image`, `Shader`, read-backs (§1) |
| `internal/shaderir/spirv` | Kage's IR → a SPIR-V module (§6); no Vulkan in it, testable on its own |

## Decisions

**Vulkan 1.3 as the baseline.** Dynamic rendering (no render-pass or framebuffer objects) and
synchronization2 are core there, which keeps the driver much smaller; lavapipe, Mesa's hardware drivers and
Crostini's Venus all offer 1.3. A device without 1.3 makes `Initialize` fail, and Ebitengine falls back as
it does for any driver that can't start.

**Frames in flight: two.** Each has a command buffer, a fence, a descriptor pool, and a ring of host-visible
coherent memory mapped once, which holds that frame's vertices, indices and uniforms. `End` submits (§10);
before a frame's slot is reused its fence is waited on and its ring and pool reset — the Metal driver's
per-frame buffer recycling, in Vulkan's shape.

**Draws.** `SetVertices` copies the batch's vertices and indices into the ring; `DrawTriangles` binds them
once per batch and issues one indexed draw per destination region, each with a scissor. A run of draws to the
same destination is one `vkCmdBeginRendering` … `vkCmdEndRendering`; switching destination ends it and adds
the image layout barriers between "colour attachment" and "shader read".

**Pipelines** are cached by (shader, blend, destination format): the blend maps one to one onto
`VkPipelineColorBlendAttachmentState` (§1); the vertex input is always Ebitengine's fixed 12-float format,
one binding and four attributes (§7); viewport and scissor are dynamic state.

**Uniforms** go into the frame's ring in **std140** layout, repacked from Ebitengine's `[]uint32` as the Metal
and DirectX drivers repack into theirs (§7), bound with a dynamic offset. They are too big for guaranteed
push-constant space (184 bytes with four sources against 128).

**Y axis** (corrected 2026-11-06, found by the first compiled-shader draw): Ebitengine's projection is taken
**as it is** — no flip. Its NDC y = −1 is pixel row 0; Vulkan's NDC y points down like its framebuffer, so
NDC −1 is memory row 0, the same rows OpenGL's offscreen textures get (bottom-up storage, NDC up). Metal and
DirectX flip because their NDC points up over a top-down framebuffer; Vulkan has neither mismatch, for
offscreen images or the swapchain (row 0 shown at the top). The first plan here — negate Y as Metal does —
would have drawn every image upside down; TestCompiledVertexShader checks both ways.

**Textures**: each source image is a combined image sampler with nearest filtering and clamp-to-edge
addressing (Ebitengine samples texels exactly — `__texelAt`); one descriptor set per draw from the frame's
pool, holding the uniform buffer and up to `ShaderSrcImageCount` images.

**Images** are `VK_FORMAT_R8G8B8A8_UNORM`, optimally tiled, in device-local memory; `WritePixels` goes
through the frame's ring and `vkCmdCopyBufferToImage`. `ReadPixelsAsync` records a `vkCmdCopyImageToBuffer`
into a host-visible buffer of its own at the current point of the stream; `Poll` checks the submit's fence
without waiting, `Wait` waits on it, and the pixels are copied out then — the "never wait for the GPU"
contract (§1).

**The screen** is a swapchain on a `VkSurfaceKHR` made from the window's X11 display and window
(`vkCreateXlibSurfaceKHR`, §3). Its image is acquired at the first draw to the screen in a frame (waiting
on an image-available semaphore), presented after the submit of `End(FlushModePresent)` (on a
render-finished semaphore). Vsync on: FIFO; off: MAILBOX where offered, else IMMEDIATE. A resized window, or
`VK_ERROR_OUT_OF_DATE_KHR`, recreates the swapchain. `NeedsClearingScreen` is false: the screen image is
cleared on load.

**Device loss** (`VK_ERROR_DEVICE_LOST`) is reported through `Resetter`, which recreates the device —
Ebitengine already knows how to rebuild its images after a driver reset.

**Shaders**: `NewShader` hands the `shaderir.Program` to `spirv.Compile`, which returns a vertex and a
fragment module; the driver makes `VkShaderModule`s of them. The SPIR-V backend writes the binary itself:
a type / constant table with de-duplication, structured control flow for Kage's `if` and fixed-form `for`,
locals as function-storage variables, the maths through `GLSL.std.450`, short-circuit `&&` / `||` as real
control flow (§6).

## Testing

- `internal/shaderir/spirv`: every module it writes in its tests passes `spirv-val` (when the tool is
  installed; skipped otherwise), and the existing Kage test programs compile.
- `internal/graphicsdriver/vulkan/vk`: an instance and a device, a headless image cleared and read back — on
  lavapipe, so it runs on any CI machine and on this VPS once `vulkan-swrast` is installed.
- The driver: Ebitengine's own test suites (`internal/graphicscommand`, `internal/atlas`, the
  `ebiten` package's image tests) run with `EBITENGINE_GRAPHICS_LIBRARY=vulkan` on lavapipe, compared against
  the same tests on OpenGL.
- A real GPU: a Chromebook in Crostini (Venus) — the case that started this — running goloco and
  GameViber's player on the branch.

## Order of work

1. `vk`: the loader and an instance / device / image test on lavapipe.
2. `spirv`: types, constants, expressions, then control flow, then the built-ins — validated as it grows.
3. The driver, offscreen first: images, `WritePixels`, `DrawTriangles`, read-backs — Ebitengine's tests.
4. The screen: surface, swapchain, present, vsync, resize.
5. Device loss, then measuring against OpenGL (frame times on lavapipe and on a GPU).

Each step lands as a few focused commits that build and pass, with the docs updated alongside.
