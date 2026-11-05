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

package vk

import (
	"os"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// The structs here are written by hand from the Vulkan specification. Where
// the Vulkan headers are installed, check them against vulkan_core.h: the
// same fields in the same order (names compared without case or
// underscores), and the structure-type values.
func vulkanHeader(t *testing.T) string {
	t.Helper()
	for _, p := range []string{"/usr/include/vulkan/vulkan_core.h", "/usr/local/include/vulkan/vulkan_core.h"} {
		if b, err := os.ReadFile(p); err == nil {
			return string(b)
		}
	}
	t.Skip("vulkan_core.h is not installed")
	return ""
}

func normalize(s string) string { return strings.ToLower(strings.ReplaceAll(s, "_", "")) }

func TestStructsMatchHeader(t *testing.T) {
	h := vulkanHeader(t)
	field := regexp.MustCompile(`^\s+(?:const\s+)?[A-Za-z0-9_]+(?:\s*\*\s*(?:const\s*)?\*?)?\s*\**\s*([A-Za-z0-9_]+)(?:\[[A-Z0-9_]+\])?;`)
	for c, v := range map[string]any{
		"VkApplicationInfo":                ApplicationInfo{},
		"VkInstanceCreateInfo":             InstanceCreateInfo{},
		"VkDeviceQueueCreateInfo":          DeviceQueueCreateInfo{},
		"VkDeviceCreateInfo":               DeviceCreateInfo{},
		"VkPhysicalDeviceVulkan13Features": PhysicalDeviceVulkan13Features{},
		"VkQueueFamilyProperties":          QueueFamilyProperties{},
		"VkPhysicalDeviceFeatures2":        physicalDeviceFeatures2{},
	} {
		start := strings.Index(h, "typedef struct "+c+" {")
		if start < 0 {
			t.Errorf("%s: not in the header", c)
			continue
		}
		body := h[start:]
		body = body[strings.Index(body, "{")+1 : strings.Index(body, "}")]
		var want []string
		for _, line := range strings.Split(body, "\n") {
			if m := field.FindStringSubmatch(line); m != nil {
				want = append(want, m[1])
			}
		}
		typ := reflect.TypeOf(v)
		var got []string
		for i := range typ.NumField() {
			got = append(got, typ.Field(i).Name)
		}
		if c == "VkPhysicalDeviceFeatures2" { // its features struct is kept as raw VkBool32s
			want[len(want)-1] = "Features"
		}
		if len(got) != len(want) {
			t.Errorf("%s: %d fields, the header has %d: %v / %v", c, len(got), len(want), got, want)
			continue
		}
		for i := range got {
			if normalize(got[i]) != normalize(want[i]) {
				t.Errorf("%s field %d: %s, the header has %s", c, i, got[i], want[i])
			}
		}
	}
	// VkPhysicalDeviceFeatures is 55 VkBool32s.
	if start := strings.Index(h, "typedef struct VkPhysicalDeviceFeatures {"); start >= 0 {
		body := h[start:]
		body = body[:strings.Index(body, "}")]
		if n := strings.Count(body, "VkBool32"); n != len(physicalDeviceFeatures2{}.Features) {
			t.Errorf("VkPhysicalDeviceFeatures has %d VkBool32s, physicalDeviceFeatures2 keeps %d", n, len(physicalDeviceFeatures2{}.Features))
		}
	}
}

func TestStructureTypesMatchHeader(t *testing.T) {
	h := vulkanHeader(t)
	for c, v := range map[string]StructureType{
		"VK_STRUCTURE_TYPE_APPLICATION_INFO":                    StructureTypeApplicationInfo,
		"VK_STRUCTURE_TYPE_INSTANCE_CREATE_INFO":                StructureTypeInstanceCreateInfo,
		"VK_STRUCTURE_TYPE_DEVICE_QUEUE_CREATE_INFO":            StructureTypeDeviceQueueCreateInfo,
		"VK_STRUCTURE_TYPE_DEVICE_CREATE_INFO":                  StructureTypeDeviceCreateInfo,
		"VK_STRUCTURE_TYPE_PHYSICAL_DEVICE_VULKAN_1_3_FEATURES": StructureTypePhysicalDeviceVulkan13Features,
		"VK_STRUCTURE_TYPE_PHYSICAL_DEVICE_FEATURES_2":          StructureTypePhysicalDeviceFeatures2,
	} {
		m := regexp.MustCompile(`\b` + c + ` = (\d+),`).FindStringSubmatch(h)
		if m == nil {
			t.Errorf("%s: not in the header", c)
			continue
		}
		if m[1] != strconv.FormatUint(uint64(v), 10) {
			t.Errorf("%s = %s in the header, %d here", c, m[1], v)
		}
	}
}
