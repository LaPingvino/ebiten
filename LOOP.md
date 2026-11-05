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
V0. [x] (DONE 2026-11-05: docs/vulkan/RESEARCH.md §1–10) **Research** (docs/vulkan/RESEARCH.md): the graphicsdriver interface in detail (what metal / directx do
    per method: command batches, FlushMode, ReadPixelsAsync, the screen framebuffer, MaxImageSize, vsync,
    transparency); how internal/ui picks a driver (EBITENGINE_GRAPHICS_LIBRARY) and how the Linux window is made
    (internal/glfw — does it expose glfwCreateWindowSurface / the X11 / Wayland handles for a VkSurfaceKHR?);
    shaderir's IR and what each backend emits (msl as the closest model); Ebitengine's own discussions of Vulkan
    (issues / the author's stance — read, honest fetches only); lavapipe on the VPS for headless tests.
V1. [x] (WRITTEN 2026-11-05: docs/vulkan/DESIGN.md — for Joop to read; building starts with V2 meanwhile, reversibly) **Design** (docs/vulkan/DESIGN.md) for Joop: the purego Vulkan loader (which functions), the SPIR-V
    emitter's shape, the driver's structure (one queue, a frame's command buffer, staging buffers, pipelines per
    shader × blend × dst format, descriptor sets, the swapchain), what's out of scope at first (Wayland? only
    X11?), how it's tested. Then build.
V2. [ ] (STARTED 2026-11-05: loader, instance, devices, properties, queue families, the logical device with 1.3 features — tested on lavapipe) The purego Vulkan loader + an instance / device / headless image test on lavapipe.
V3. [ ] SPIR-V backend for shaderir, checked with spirv-val where available (a test tool, not a run-time dependency).
V4. [ ] The driver: images, DrawTriangles, WritePixels / ReadPixelsAsync — Ebitengine's own tests with
    EBITENGINE_GRAPHICS_LIBRARY=vulkan.
V5. [ ] The screen: the swapchain through glfw on X11; then Wayland.
V6. [ ] goloco and GameViber on it (a replace directive in a local build); Joop tries it in Crostini.

## Questions for Joop

## Blocked
- (none) — RESOLVED 2026-11-05: Joop installed lavapipe at 17:26 (vulkan-swrast 26.2.6 owns lvp_icd.json; pacman.log); the
  device here is "llvmpipe (LLVM 23.1.4, 256 bits)", Vulkan 1.4, one queue family with graphics. Driver tests
  run on this VPS.

## Tick log
- 2026-11-05 — set up: clone of hajimehoshi/ebiten (main, c487dc97), branch `vulkan`, this LOOP. Sizing: the
  driver interface is ~15 methods; Metal's driver with its bindings ~5,000 lines; the MSL shader backend 745.
- 2026-11-05 — public at github.com/LaPingvino/ebiten (fork; branch `vulkan`), Joop's yes; showcase quality the bar, upstreaming not the goal.
- 2026-11-05 (V0 research, part 1 — overnight) — docs/vulkan/RESEARCH.md: the driver contract method by method (FlushMode → submit / present, ReadPixelsAsync's no-wait rule → copy + fence, Blend ↔ VkPipelineColorBlendAttachmentState, Resetter ↔ device lost), how internal/ui picks a driver (EBITENGINE_GRAPHICS_LIBRARY, the creator interface — what a "vulkan" choice needs), the Linux window (Ebitengine's own pure-Go X11 GLFW; GetX11Display / GetX11Window are what vkCreateXlibSurfaceKHR takes — no window-layer change), sizes, test tools here (spirv-val / spirv-dis yes; lavapipe no — Blocked, sudo). NEXT: part 2 — shaderir and the msl backend, the opengl / metal frame organisation, Ebitengine's Vulkan discussions.
- 2026-11-05 (V0 research, part 2 — overnight) — RESEARCH.md §6: the shader IR (types, 10 statement kinds, 15 expression kinds, 56 built-ins) mapped onto SPIR-V: types one to one, structured control flow (selection / loop merges; Kage's fixed for-loop form → one canonical loop shape), locals as Function-storage variables (no SSA needed), the maths through GLSL.std.450, derivatives to OpDPdx / OpDPdy / OpFwidth, mod → OpFMod (GLSL's sign), __texelAt → an explicit-LOD-0 sample; && / || need care (short-circuit). NEXT: part 3 — uniform packing and the vertex layout per driver, the opengl / metal frame organisation, Ebitengine's Vulkan discussions, purego's limits for a struct-heavy C API.
- 2026-11-05 (V0 research, part 3 — overnight) — RESEARCH.md §7: uniforms arrive packed in IR order with Ebitengine's 7 preserved uniforms first (184 bytes with four sources — too big for guaranteed push constants, so a per-frame uniform buffer with dynamic offsets); how Metal (alignment, padded vec3 / mat3, Y-flipped projection) and DirectX (HLSL packing, transposed matrices) repack; Vulkan: std140 and a decision on Y (negate the projection as Metal, or a negative viewport height); the fixed 12-float vertex format → one binding, four attributes, one vertex + index buffer per frame. NEXT: part 4 — a frame in the metal driver (command buffers, the screen image, read-backs), Ebitengine's Vulkan discussions, purego's limits.
- 2026-11-05 (V0 research, part 4 — overnight) — RESEARCH.md §8: hajimehoshi/ebiten#389 (open since 2017): the author's expected benefits (speed, restoring, triple buffering), "no concrete plan", "too early … wait and see", Android notes, and (2024) "As we have removed the restorable package, the priority got lower" — no opposition, no plan; the arguments that still hold (GPU where GL falls back to software but Vulkan doesn't, presentation control, Android). §9: purego and Vulkan's struct-heavy API (structs by address, functions through vkGet*ProcAddr after Dlopen of the loader, runtime.Pinner for pNext chains and arrays, NewCallback only for the debug messenger, handle sizes). NEXT: the last research part — a frame in the metal driver (command buffers, the screen image, read-backs) — then DESIGN.md (V1).
- 2026-11-05 (V0 done — overnight) — RESEARCH.md §8 gains Joop's point (the author's objection was implementation cost, weighed before LLMs — this branch tests whether it still holds); §10: a frame as the Metal driver runs it (one command buffer committed at every End, present on FlushModePresent; an encoder per destination; pooled per-frame vertex / index buffers recycled when the frame is done; the drawable fetched at the first screen draw) and the Vulkan shape of each (submit + fence per End, dynamic rendering per destination with layout barriers, a mapped ring per frame in flight, acquire / present semaphores, copy-to-buffer read-backs). NEXT: V1 — DESIGN.md.
- 2026-11-05 (V1: the design — overnight) — docs/vulkan/DESIGN.md: scope (Linux / BSD, amd64 / arm64, X11 + XWayland first; native Wayland, Windows, Apple, Android, 32-bit later), the three packages (vk bindings, the driver, shaderir/spirv), and the decisions with their reasons: Vulkan 1.3 baseline (dynamic rendering, synchronization2), two frames in flight with a mapped ring each, render runs per destination with layout barriers, pipelines cached by shader × blend × format, std140 uniforms with dynamic offsets (too big for push constants), the projection's Y negated as Metal does, nearest clamp-to-edge samplers, R8G8B8A8 images with staged uploads and fenced read-backs, the swapchain from the X11 window (FIFO / MAILBOX / IMMEDIATE), device loss through Resetter; testing (spirv-val, lavapipe, Ebitengine's own suites with EBITENGINE_GRAPHICS_LIBRARY=vulkan, Crostini); the order of work. NEXT: V2 — the vk loader (writing it doesn't need lavapipe; running its test does — Blocked item).
- 2026-11-05 (V2 part 1 — overnight) — internal/graphicsdriver/vulkan/vk: libvulkan.so.1 through purego's Dlopen / Dlsym (vkGetInstanceProcAddr only; everything else through it), Result as an error type, the API-version helpers, ApplicationInfo / InstanceCreateInfo in C layout, EnumerateInstanceVersion, CreateInstance (strings and structs pinned with runtime.Pinner for the call), DestroyInstance, EnumeratePhysicalDevices. Tests run here: the loader offers Vulkan 1.4.363; an instance at 1.3 is created and destroyed; it lists 1 physical device — although only radeon's ICD is installed: to identify (vkGetPhysicalDeviceProperties) — it may mean device tests can run here without lavapipe; a missing extension comes back as VK_ERROR_EXTENSION_NOT_PRESENT. NEXT: physical-device properties and queue families, then a logical device.
- 2026-11-05 (V2 part 2 — overnight) — vkGetPhysicalDeviceProperties (header fields and maxImageDimension2D read from the 824-byte C struct at fixed 64-bit offsets, so the large limits struct needn't be mirrored yet) and vkGetPhysicalDeviceQueueFamilyProperties. The test now names the device: lavapipe ("llvmpipe (LLVM 23.1.4, 256 bits)", CPU type, Vulkan 1.4, max 2D image 16384, one queue family with graphics/compute/transfer) — Joop installed vulkan-swrast at 17:26 (pacman.log; an earlier "installed all along" was wrong); the Blocked item is gone. NEXT: the logical device and its queue (vkCreateDevice with dynamic rendering, 1.3 features), then a headless image cleared to a colour and read back.
- 2026-11-05 (V2 part 3 — overnight) — the logical device: GetPhysicalDeviceVulkan13Features (vkGetPhysicalDeviceFeatures2 with VkPhysicalDeviceVulkan13Features chained), CreateDevice (one queue, the 1.3 features chained into pNext, extensions), vkGetDeviceProcAddr for device functions, GetDeviceQueue, DeviceWaitIdle, DestroyDevice. Tests on lavapipe: dynamicRendering / synchronization2 supported, a device made with them, its queue, idle, destroyed; an unknown device extension refused. CAVEAT: lavapipe has every 1.3 feature, so the 15-field order of the features struct is not proven by this test — check it with the validation layers (vulkan-validation-layers; not installed: optional, Joop) or against vulkan_core.h. NEXT: memory types, a buffer and an image; then a command pool / buffer clearing an image and reading it back (the first pixels).
- 2026-11-05 (V2 part 4 — overnight) — the caveat closed: header_test.go checks every hand-written struct against vulkan_core.h where the headers are installed (they are here: vulkan-headers) — the same fields in the same order (names without case / underscores), VkPhysicalDeviceFeatures' 55 VkBool32s, and the structure-type values; the test skips where there is no header. Proven able to fail: two Vulkan13Features fields swapped and a wrong structure type each reported, then restored. The 1.3 features struct matches. RULE from here: every new binding struct and enum goes into that test's table. NEXT: memory types, a buffer and an image; then a command buffer clearing an image and reading it back.

