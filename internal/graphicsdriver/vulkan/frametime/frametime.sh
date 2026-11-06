#!/bin/sh
# Frame times on OpenGL and Vulkan, both scenes (LOOP V8). On a desktop with a
# GPU run it as is; on a headless box: xvfb-run -a sh frametime.sh.
# Extra arguments go to the program (e.g. -frames 1200 -n 20000).
set -e
cd "$(dirname "$0")/../../../.."
go build -o /tmp/frametime-$$ ./internal/graphicsdriver/vulkan/frametime
trap 'rm -f /tmp/frametime-$$' EXIT
command -v vulkaninfo >/dev/null && vulkaninfo --summary 2>/dev/null | grep -m1 deviceName || true
for scene in sprites shader; do
	for lib in opengl vulkan; do
		EBITENGINE_GRAPHICS_LIBRARY=$lib /tmp/frametime-$$ -scene $scene "$@"
	done
done
