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

// testInstance is a Vulkan 1.3 instance and its first device with a
// graphics queue, or a skip where there is none.
func testInstance(t *testing.T) (vk.Instance, vk.PhysicalDevice, uint32) {
	t.Helper()
	if v, err := vk.EnumerateInstanceVersion(); err != nil || v < vk.APIVersion13 {
		t.Skipf("no Vulkan 1.3 loader (%v)", err)
	}
	instance, err := vk.CreateInstance("ebitengine-test", vk.APIVersion13, nil)
	if errors.Is(err, vk.ErrorIncompatibleDriver) {
		t.Skip("no Vulkan 1.3 driver installed")
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { vk.DestroyInstance(instance) })
	devices, err := vk.EnumeratePhysicalDevices(instance)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range devices {
		if vk.GetPhysicalDeviceProperties(instance, d).APIVersion < vk.APIVersion13 {
			continue
		}
		for i, f := range vk.GetPhysicalDeviceQueueFamilyProperties(instance, d) {
			if f.QueueFlags&vk.QueueGraphicsBit != 0 && f.QueueCount > 0 {
				return instance, d, uint32(i)
			}
		}
	}
	t.Skip("no Vulkan 1.3 device with a graphics queue")
	return 0, 0, 0
}

// The device the driver needs: dynamic rendering and synchronization2
// supported, a logical device made with them, its queue, idle, destroyed.
func TestDevice(t *testing.T) {
	instance, physical, family := testInstance(t)
	f, err := vk.GetPhysicalDeviceVulkan13Features(instance, physical)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("1.3 features: dynamicRendering %d, synchronization2 %d, maintenance4 %d", f.DynamicRendering, f.Synchronization2, f.Maintenance4)
	if f.DynamicRendering == 0 || f.Synchronization2 == 0 {
		t.Fatal("a Vulkan 1.3 device must support dynamicRendering and synchronization2")
	}
	device, err := vk.CreateDevice(instance, physical, family, &vk.PhysicalDeviceVulkan13Features{DynamicRendering: 1, Synchronization2: 1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer vk.DestroyDevice(instance, device)
	if q := vk.GetDeviceQueue(instance, device, family, 0); q == 0 {
		t.Error("no queue")
	}
	if err := vk.DeviceWaitIdle(instance, device); err != nil {
		t.Error(err)
	}
}

// An unknown device extension is refused with VK_ERROR_EXTENSION_NOT_PRESENT.
func TestDeviceMissingExtension(t *testing.T) {
	instance, physical, family := testInstance(t)
	_, err := vk.CreateDevice(instance, physical, family, nil, []string{"VK_EBITENGINE_no_such_extension"})
	if !errors.Is(err, vk.ErrorExtensionNotPresent) {
		t.Fatalf("got %v, want VK_ERROR_EXTENSION_NOT_PRESENT", err)
	}
}
