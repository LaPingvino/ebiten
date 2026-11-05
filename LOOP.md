# Ebitengine Vulkan backend — loop

Started 2026-11-05 (Joop: "Add a third loop element to write an Ebitengine Vulkan backend 😉 (just like the Opus
thing we did...)"). Why: on Linux, Ebitengine draws only through OpenGL; where OpenGL falls back to llvmpipe
but Vulkan reaches the GPU (Crostini on Joop's Chromebook: Zed fast, goloco's GL on the CPU), a Vulkan driver
would give Ebitengine games — goloco, GameViber's player — the real GPU. Zink (Mesa's GL-on-Vulkan) is the
workaround meanwhile.

Public: github.com/LaPingvino/ebiten, branch `vulkan` (Joop 2026-11-05: "make it public. I kinda want to make it
attractive enough that people stop considering all AI work slop ;). So don't count on it being upstreamed
either."). So the bar is the work itself: code a maintainer would be glad to read (Ebitengine's style, small
focused commits with honest messages, tests, docs that say what works and what doesn't), not a pull request.

This is a branch (`vulkan`) of hajimehoshi/ebiten: the graphics drivers are internal packages
(internal/graphicsdriver/…), so a backend can't live in a module of its own. Aim: a driver good enough to
stand next to the existing drivers — Ebitengine's style, its licence (Apache-2.0), its tests. Upstreaming is
not the goal (Joop) — but nothing in it should stand in the way.

Each tick: read this file, do ONE bounded chunk (~10 min), verify it, commit, update the queue + one tick-log
line. Rotation: ~/Projects/empty/.idle-loop-next (goloco → gameviber → ebiten-vulkan → goloco).

## Non-negotiables
- **Upstream Ebitengine's design is the frame.** The driver implements graphicsdriver.Graphics / Image / Shader
  as the existing ones do (metal, directx, opengl are the models); no changes outside what a new driver needs,
  and each such change argued in DESIGN.md.
- **No cgo for Vulkan** — purego, as Ebitengine's own OpenGL and Metal bindings (it is already a dependency).
  No new Go modules without Joop's yes.
- **Kage stays the shader language**: a SPIR-V backend for internal/shaderir (beside glsl / hlsl / msl / pssl),
  written here — no glslang or other external compiler at run time.
- **Verified on a GPU we can see**: tests against Vulkan's software driver (lavapipe) here on the VPS, and
  Joop's Chromebook (Crostini's Venus) for the real thing.
- **Showcase quality** (Joop): every commit something to be proud of — no half-finished stubs pushed as done,
  no generated filler, measured claims only. Push to origin (the fork) after each verified tick; `upstream`
  (hajimehoshi/ebiten) has its push URL disabled.

## Queue (top = next)
V0. [ ] **Research** (docs/vulkan/RESEARCH.md): the graphicsdriver interface in detail (what metal / directx do
    per method: command batches, FlushMode, ReadPixelsAsync, the screen framebuffer, MaxImageSize, vsync,
    transparency); how internal/ui picks a driver (EBITENGINE_GRAPHICS_LIBRARY) and how the Linux window is made
    (internal/glfw — does it expose glfwCreateWindowSurface / the X11 / Wayland handles for a VkSurfaceKHR?);
    shaderir's IR and what each backend emits (msl as the closest model); Ebitengine's own discussions of Vulkan
    (issues / the author's stance — read, honest fetches only); lavapipe on the VPS for headless tests.
V1. [ ] **Design** (docs/vulkan/DESIGN.md) for Joop: the purego Vulkan loader (which functions), the SPIR-V
    emitter's shape, the driver's structure (one queue, a frame's command buffer, staging buffers, pipelines per
    shader × blend × dst format, descriptor sets, the swapchain), what's out of scope at first (Wayland? only
    X11?), how it's tested. Then build.
V2. [ ] The purego Vulkan loader + an instance / device / headless image test on lavapipe.
V3. [ ] SPIR-V backend for shaderir, checked with spirv-val where available (a test tool, not a run-time dependency).
V4. [ ] The driver: images, DrawTriangles, WritePixels / ReadPixelsAsync — Ebitengine's own tests with
    EBITENGINE_GRAPHICS_LIBRARY=vulkan.
V5. [ ] The screen: the swapchain through glfw on X11; then Wayland.
V6. [ ] goloco and GameViber on it (a replace directive in a local build); Joop tries it in Crostini.

## Questions for Joop

## Blocked
- Testing on this VPS needs lavapipe (Mesa's software Vulkan): `sudo pacman -S vulkan-swrast` (Joop). Until
  then V2's device test can only be written, not run here.

## Tick log
- 2026-11-05 — set up: clone of hajimehoshi/ebiten (main, c487dc97), branch `vulkan`, this LOOP. Sizing: the
  driver interface is ~15 methods; Metal's driver with its bindings ~5,000 lines; the MSL shader backend 745.
- 2026-11-05 — public at github.com/LaPingvino/ebiten (fork; branch `vulkan`), Joop's yes; showcase quality the bar, upstreaming not the goal.
- 2026-11-05 (V0 research, part 1 — overnight) — docs/vulkan/RESEARCH.md: the driver contract method by method (FlushMode → submit / present, ReadPixelsAsync's no-wait rule → copy + fence, Blend ↔ VkPipelineColorBlendAttachmentState, Resetter ↔ device lost), how internal/ui picks a driver (EBITENGINE_GRAPHICS_LIBRARY, the creator interface — what a "vulkan" choice needs), the Linux window (Ebitengine's own pure-Go X11 GLFW; GetX11Display / GetX11Window are what vkCreateXlibSurfaceKHR takes — no window-layer change), sizes, test tools here (spirv-val / spirv-dis yes; lavapipe no — Blocked, sudo). NEXT: part 2 — shaderir and the msl backend, the opengl / metal frame organisation, Ebitengine's Vulkan discussions.
- 2026-11-05 (V0 research, part 2 — overnight) — RESEARCH.md §6: the shader IR (types, 10 statement kinds, 15 expression kinds, 56 built-ins) mapped onto SPIR-V: types one to one, structured control flow (selection / loop merges; Kage's fixed for-loop form → one canonical loop shape), locals as Function-storage variables (no SSA needed), the maths through GLSL.std.450, derivatives to OpDPdx / OpDPdy / OpFwidth, mod → OpFMod (GLSL's sign), __texelAt → an explicit-LOD-0 sample; && / || need care (short-circuit). NEXT: part 3 — uniform packing and the vertex layout per driver, the opengl / metal frame organisation, Ebitengine's Vulkan discussions, purego's limits for a struct-heavy C API.
- 2026-11-05 (V0 research, part 3 — overnight) — RESEARCH.md §7: uniforms arrive packed in IR order with Ebitengine's 7 preserved uniforms first (184 bytes with four sources — too big for guaranteed push constants, so a per-frame uniform buffer with dynamic offsets); how Metal (alignment, padded vec3 / mat3, Y-flipped projection) and DirectX (HLSL packing, transposed matrices) repack; Vulkan: std140 and a decision on Y (negate the projection as Metal, or a negative viewport height); the fixed 12-float vertex format → one binding, four attributes, one vertex + index buffer per frame. NEXT: part 4 — a frame in the metal driver (command buffers, the screen image, read-backs), Ebitengine's Vulkan discussions, purego's limits.

