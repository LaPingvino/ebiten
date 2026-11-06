# Trying the Vulkan driver with a game

The driver lives on branch `vulkan` of github.com/LaPingvino/ebiten. A game
can be built against it without touching its own `go.mod`: a copy of it with
a `replace` line, passed with `-modfile`.

```sh
git clone -b vulkan https://github.com/LaPingvino/ebiten ~/ebiten-vulkan
cd /path/to/your/game
cp go.mod /tmp/vk.mod && cp go.sum /tmp/vk.sum
echo 'replace github.com/hajimehoshi/ebiten/v2 => '"$HOME"'/ebiten-vulkan' >> /tmp/vk.mod
go build -mod=mod -modfile=/tmp/vk.mod -o /tmp/game-vk .
EBITENGINE_GRAPHICS_LIBRARY=vulkan /tmp/game-vk
```

- `EBITENGINE_GRAPHICS_LIBRARY=vulkan` chooses the driver (it is never chosen
  automatically yet); without it the game runs on OpenGL as before.
- `EBITENGINE_VULKAN_VALIDATION=1` turns on the Khronos validation layer
  (install `vulkan-validation-layers`); its messages go to stderr.
- `vulkaninfo --summary` shows which device Vulkan sees: on a Chromebook's
  Linux (Crostini) it should name the GPU through Venus (virtio-gpu), not
  `llvmpipe`.

The window is X11 (on Wayland desktops, and Crostini, through XWayland —
Ebitengine's window is always X11 on Linux). Vsync follows the game's
setting (FIFO; MAILBOX or IMMEDIATE when it is off).

Checked so far (Xvfb with Mesa's lavapipe, validation on and silent):
Ebitengine's own tests (the ebiten package 209/209, graphicscommand, atlas,
buffered), its examples 2048 and shader, goloco's title screen and a
GameViber starter game — the last two pixel-identical to OpenGL.
