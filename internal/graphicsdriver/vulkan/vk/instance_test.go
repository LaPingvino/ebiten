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

package vk_test

import (
	"errors"
	"testing"

	"github.com/hajimehoshi/ebiten/v2/internal/graphicsdriver/vulkan/vk"
)

// The loader reports its version, an instance can be made and destroyed, and
// the instance lists its devices (none is a valid answer on a machine without
// a Vulkan driver; the test then says so).
func TestInstance(t *testing.T) {
	v, err := vk.EnumerateInstanceVersion()
	if err != nil {
		t.Skipf("no Vulkan loader: %v", err)
	}
	t.Logf("loader: Vulkan %d.%d.%d", vk.VersionMajor(v), vk.VersionMinor(v), vk.VersionPatch(v))
	if v < vk.APIVersion13 {
		t.Skipf("the loader offers Vulkan %d.%d; the driver needs 1.3", vk.VersionMajor(v), vk.VersionMinor(v))
	}

	instance, err := vk.CreateInstance("ebitengine-test", vk.APIVersion13, nil)
	if errors.Is(err, vk.ErrorIncompatibleDriver) {
		t.Skip("no Vulkan 1.3 driver installed")
	}
	if err != nil {
		t.Fatal(err)
	}
	defer vk.DestroyInstance(instance)

	devices, err := vk.EnumeratePhysicalDevices(instance)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%d physical device(s)", len(devices))
	for _, d := range devices {
		p := vk.GetPhysicalDeviceProperties(instance, d)
		t.Logf("  %q (%s), Vulkan %d.%d, vendor %#x, max 2D image %d", p.DeviceName, p.DeviceType,
			vk.VersionMajor(p.APIVersion), vk.VersionMinor(p.APIVersion), p.VendorID, p.MaxImageDimension2D)
		if p.DeviceName == "" || p.MaxImageDimension2D < 4096 {
			t.Errorf("properties read wrong: %+v (Vulkan guarantees a 2D image of at least 4096)", p)
		}
		families := vk.GetPhysicalDeviceQueueFamilyProperties(instance, d)
		graphics := false
		for i, f := range families {
			t.Logf("  queue family %d: flags %#x, %d queue(s)", i, f.QueueFlags, f.QueueCount)
			graphics = graphics || f.QueueFlags&vk.QueueGraphicsBit != 0 && f.QueueCount > 0
		}
		if !graphics {
			t.Errorf("%q has no graphics queue", p.DeviceName)
		}
	}
}

// Asking for an extension no driver has fails with
// VK_ERROR_EXTENSION_NOT_PRESENT — the error comes back as a vk.Result.
func TestInstanceMissingExtension(t *testing.T) {
	if _, err := vk.EnumerateInstanceVersion(); err != nil {
		t.Skipf("no Vulkan loader: %v", err)
	}
	_, err := vk.CreateInstance("ebitengine-test", vk.APIVersion13, []string{"VK_EBITENGINE_no_such_extension"})
	if !errors.Is(err, vk.ErrorExtensionNotPresent) && !errors.Is(err, vk.ErrorIncompatibleDriver) {
		t.Fatalf("got %v, want VK_ERROR_EXTENSION_NOT_PRESENT", err)
	}
}
