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
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
)

// VK_EXT_debug_utils: the validation layers' messages, handed to Go.

// LayerKhronosValidation is the validation layer's name.
const LayerKhronosValidation = "VK_LAYER_KHRONOS_validation"

// ExtDebugUtils is the extension's name.
const ExtDebugUtils = "VK_EXT_debug_utils"

// DebugUtilsMessengerEXT is VkDebugUtilsMessengerEXT (a non-dispatchable handle).
type DebugUtilsMessengerEXT uint64

const (
	StructureTypeDebugUtilsMessengerCallbackDataEXT StructureType = 1000128003
	StructureTypeDebugUtilsMessengerCreateInfoEXT   StructureType = 1000128004
)

// Message severities and types (VkDebugUtilsMessageSeverityFlagBitsEXT,
// VkDebugUtilsMessageTypeFlagBitsEXT).
const (
	DebugUtilsMessageSeverityVerboseBitEXT = 0x00000001
	DebugUtilsMessageSeverityInfoBitEXT    = 0x00000010
	DebugUtilsMessageSeverityWarningBitEXT = 0x00000100
	DebugUtilsMessageSeverityErrorBitEXT   = 0x00001000

	DebugUtilsMessageTypeGeneralBitEXT     = 0x00000001
	DebugUtilsMessageTypeValidationBitEXT  = 0x00000002
	DebugUtilsMessageTypePerformanceBitEXT = 0x00000004
)

// DebugUtilsMessengerCreateInfoEXT is VkDebugUtilsMessengerCreateInfoEXT.
type DebugUtilsMessengerCreateInfoEXT struct {
	SType           StructureType
	PNext           uintptr
	Flags           uint32
	MessageSeverity uint32
	MessageType     uint32
	PfnUserCallback uintptr
	PUserData       uintptr
}

// DebugUtilsMessengerCallbackDataEXT is VkDebugUtilsMessengerCallbackDataEXT.
type DebugUtilsMessengerCallbackDataEXT struct {
	SType            StructureType
	PNext            uintptr
	Flags            uint32
	PMessageIDName   *byte
	MessageIDNumber  int32
	PMessage         *byte
	QueueLabelCount  uint32
	PQueueLabels     uintptr
	CmdBufLabelCount uint32
	PCmdBufLabels    uintptr
	ObjectCount      uint32
	PObjects         uintptr
}

// DebugMessage is one message from a layer.
type DebugMessage struct {
	Severity, Type uint32
	ID, Text       string
}

var (
	debugOnce     sync.Once
	debugCallback uintptr
	debugMu       sync.Mutex
	debugHandlers = map[uintptr]func(DebugMessage){}
	debugNext     uintptr
)

// gostr copies a NUL-terminated C string (the layer owns the memory only
// for the call).
func gostr(p *byte) string {
	if p == nil {
		return ""
	}
	var n int
	for *(*byte)(unsafe.Add(unsafe.Pointer(p), n)) != 0 {
		n++
	}
	return string(unsafe.Slice(p, n))
}

// debugTrampoline is the one C callback; pUserData picks the handler.
func debugTrampoline(severity, types uint32, data *DebugUtilsMessengerCallbackDataEXT, user uintptr) uintptr {
	debugMu.Lock()
	h := debugHandlers[user]
	debugMu.Unlock()
	if h != nil && data != nil {
		h(DebugMessage{Severity: severity, Type: types, ID: gostr(data.PMessageIDName), Text: gostr(data.PMessage)})
	}
	return 0 // VK_FALSE: the call that caused it goes on
}

// CreateDebugUtilsMessenger is vkCreateDebugUtilsMessengerEXT: messages of
// the given severities and types go to handle, until
// DestroyDebugUtilsMessenger. The instance needs VK_EXT_debug_utils.
func CreateDebugUtilsMessenger(instance Instance, severities, types uint32, handle func(DebugMessage)) (DebugUtilsMessengerEXT, error) {
	fn := procAddr(instance, "vkCreateDebugUtilsMessengerEXT")
	if fn == 0 {
		return 0, fmt.Errorf("vk: vkCreateDebugUtilsMessengerEXT is not available (is %s enabled?)", ExtDebugUtils)
	}
	debugOnce.Do(func() { debugCallback = purego.NewCallback(debugTrampoline) })
	debugMu.Lock()
	debugNext++
	user := debugNext
	debugHandlers[user] = handle
	debugMu.Unlock()

	info := &DebugUtilsMessengerCreateInfoEXT{
		SType:           StructureTypeDebugUtilsMessengerCreateInfoEXT,
		MessageSeverity: severities,
		MessageType:     types,
		PfnUserCallback: debugCallback,
		PUserData:       user,
	}
	var pinner runtime.Pinner
	defer pinner.Unpin()
	pinner.Pin(info)
	var m DebugUtilsMessengerEXT
	r, _, _ := purego.SyscallN(fn, uintptr(instance), uintptr(unsafe.Pointer(info)), 0, uintptr(unsafe.Pointer(&m)))
	if err := result(r); err != nil {
		debugMu.Lock()
		delete(debugHandlers, user)
		debugMu.Unlock()
		return 0, err
	}
	debugMu.Lock()
	debugMessengers[m] = user
	debugMu.Unlock()
	return m, nil
}

var debugMessengers = map[DebugUtilsMessengerEXT]uintptr{}

// DestroyDebugUtilsMessenger is vkDestroyDebugUtilsMessengerEXT.
func DestroyDebugUtilsMessenger(instance Instance, m DebugUtilsMessengerEXT) {
	if fn := procAddr(instance, "vkDestroyDebugUtilsMessengerEXT"); fn != 0 {
		purego.SyscallN(fn, uintptr(instance), uintptr(m), 0)
	}
	debugMu.Lock()
	delete(debugHandlers, debugMessengers[m])
	delete(debugMessengers, m)
	debugMu.Unlock()
}
