# Ebitengine Vulkan backend — loop

Started 2026-11-05 (Joop: "Add a third loop element to write an Ebitengine Vulkan backend 😉 (just like the Opus
thing we did...)"). Why: on Linux, Ebitengine draws only through OpenGL; where OpenGL falls back to llvmpipe
but Vulkan reaches the GPU (Crostini on Joop's Chromebook: Zed fast, goloco's GL on the CPU), a Vulkan driver
would give Ebitengine games — goloco, GameViber's player — the real GPU. Zink (Mesa's GL-on-Vulkan) is the
workaround meanwhile.

This is a branch (`vulkan`) of hajimehoshi/ebiten: the graphics drivers are internal packages
(internal/graphicsdriver/…), so a backend can't live in a module of its own. Aim: a driver good enough to
offer upstream as a pull request — Ebitengine's style, its licence (Apache-2.0), its tests.

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
- **Nothing public without Joop's yes**: the repo stays local until he says where it goes (a fork on his GitHub,
  as gopus?).

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
- Where should it live: a public fork on your GitHub (LaPingvino/ebiten, branch `vulkan`, as gopus is public), or
  local only for now? (Local until you say.)

## Blocked

## Tick log
- 2026-11-05 — set up: clone of hajimehoshi/ebiten (main, c487dc97), branch `vulkan`, this LOOP. Sizing: the
  driver interface is ~15 methods; Metal's driver with its bindings ~5,000 lines; the MSL shader backend 745.
