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
	"fmt"
	"runtime"
	"unsafe"

	"github.com/ebitengine/purego"
)

// cstr returns a NUL-terminated copy of s.
func cstr(s string) *byte {
	b := make([]byte, len(s)+1)
	copy(b, s)
	return &b[0]
}

// procAddr is vkGetInstanceProcAddr(instance, name); 0 when the function is
// not available.
func procAddr(instance Instance, name string) uintptr {
	n := cstr(name)
	r, _, _ := purego.SyscallN(getInstanceProcAddr, uintptr(instance), uintptr(unsafe.Pointer(n)))
	runtime.KeepAlive(n)
	return r
}

// EnumerateInstanceVersion is vkEnumerateInstanceVersion: the highest Vulkan
// version the loader supports for instances.
func EnumerateInstanceVersion() (uint32, error) {
	if err := load(); err != nil {
		return 0, err
	}
	fn := procAddr(0, "vkEnumerateInstanceVersion")
	if fn == 0 {
		return MakeAPIVersion(0, 1, 0, 0), nil // a 1.0 loader has no such function
	}
	var v uint32
	r, _, _ := purego.SyscallN(fn, uintptr(unsafe.Pointer(&v)))
	return v, result(r)
}

// CreateInstance is vkCreateInstance, with the given application name, API
// version and instance extensions.
func CreateInstance(appName string, apiVersion uint32, extensions []string) (Instance, error) {
	if err := load(); err != nil {
		return 0, err
	}
	fn := procAddr(0, "vkCreateInstance")
	if fn == 0 {
		return 0, fmt.Errorf("vk: vkCreateInstance is not available")
	}
	var pinner runtime.Pinner
	defer pinner.Unpin()

	app := &ApplicationInfo{
		SType:            StructureTypeApplicationInfo,
		PApplicationName: cstr(appName),
		PEngineName:      cstr("Ebitengine"),
		APIVersion:       apiVersion,
	}
	pinner.Pin(app)
	pinner.Pin(app.PApplicationName)
	pinner.Pin(app.PEngineName)

	info := &InstanceCreateInfo{
		SType:            StructureTypeInstanceCreateInfo,
		PApplicationInfo: app,
	}
	if len(extensions) > 0 {
		names := make([]*byte, len(extensions))
		for i, e := range extensions {
			names[i] = cstr(e)
			pinner.Pin(names[i])
		}
		pinner.Pin(&names[0])
		info.EnabledExtensionCount = uint32(len(names))
		info.PPEnabledExtensionNames = &names[0]
	}
	pinner.Pin(info)

	var instance Instance
	r, _, _ := purego.SyscallN(fn, uintptr(unsafe.Pointer(info)), 0, uintptr(unsafe.Pointer(&instance)))
	if err := result(r); err != nil {
		return 0, err
	}
	return instance, nil
}

// DestroyInstance is vkDestroyInstance.
func DestroyInstance(instance Instance) {
	if fn := procAddr(instance, "vkDestroyInstance"); fn != 0 {
		purego.SyscallN(fn, uintptr(instance), 0)
	}
}

// EnumeratePhysicalDevices is vkEnumeratePhysicalDevices: every device the
// instance's drivers offer (none is not an error).
func EnumeratePhysicalDevices(instance Instance) ([]PhysicalDevice, error) {
	fn := procAddr(instance, "vkEnumeratePhysicalDevices")
	if fn == 0 {
		return nil, fmt.Errorf("vk: vkEnumeratePhysicalDevices is not available")
	}
	var n uint32
	r, _, _ := purego.SyscallN(fn, uintptr(instance), uintptr(unsafe.Pointer(&n)), 0)
	if err := result(r); err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, nil
	}
	devices := make([]PhysicalDevice, n)
	r, _, _ = purego.SyscallN(fn, uintptr(instance), uintptr(unsafe.Pointer(&n)), uintptr(unsafe.Pointer(&devices[0])))
	if err := result(r); err != nil && err != Incomplete {
		return nil, err
	}
	return devices[:n], nil
}
