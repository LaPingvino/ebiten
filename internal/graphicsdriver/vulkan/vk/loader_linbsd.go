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

package vk

import (
	"errors"
	"fmt"
	"sync"

	"github.com/ebitengine/purego"
)

var (
	loadOnce            sync.Once
	loadErr             error
	getInstanceProcAddr uintptr
)

// load opens the Vulkan loader and finds vkGetInstanceProcAddr, once.
func load() error {
	loadOnce.Do(func() {
		var errs []error
		for _, name := range []string{"libvulkan.so.1", "libvulkan.so"} {
			lib, err := purego.Dlopen(name, purego.RTLD_LAZY|purego.RTLD_GLOBAL)
			if err != nil {
				errs = append(errs, fmt.Errorf("vk: Dlopen failed: name: %s: %w", name, err))
				continue
			}
			p, err := purego.Dlsym(lib, "vkGetInstanceProcAddr")
			if err != nil {
				errs = append(errs, fmt.Errorf("vk: Dlsym failed: vkGetInstanceProcAddr: %w", err))
				continue
			}
			getInstanceProcAddr = p
			return
		}
		loadErr = errors.Join(errs...)
	})
	return loadErr
}
