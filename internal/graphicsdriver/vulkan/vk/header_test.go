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
		"VkApplicationInfo":                      ApplicationInfo{},
		"VkInstanceCreateInfo":                   InstanceCreateInfo{},
		"VkDeviceQueueCreateInfo":                DeviceQueueCreateInfo{},
		"VkDeviceCreateInfo":                     DeviceCreateInfo{},
		"VkPhysicalDeviceVulkan13Features":       PhysicalDeviceVulkan13Features{},
		"VkQueueFamilyProperties":                QueueFamilyProperties{},
		"VkPhysicalDeviceFeatures2":              physicalDeviceFeatures2{},
		"VkMemoryType":                           MemoryType{},
		"VkMemoryHeap":                           MemoryHeap{},
		"VkPhysicalDeviceMemoryProperties":       PhysicalDeviceMemoryProperties{},
		"VkMemoryRequirements":                   MemoryRequirements{},
		"VkMemoryAllocateInfo":                   MemoryAllocateInfo{},
		"VkBufferCreateInfo":                     BufferCreateInfo{},
		"VkCommandPoolCreateInfo":                CommandPoolCreateInfo{},
		"VkCommandBufferAllocateInfo":            CommandBufferAllocateInfo{},
		"VkCommandBufferBeginInfo":               CommandBufferBeginInfo{},
		"VkSubmitInfo":                           SubmitInfo{},
		"VkExtent3D":                             Extent3D{},
		"VkOffset3D":                             Offset3D{},
		"VkImageCreateInfo":                      ImageCreateInfo{},
		"VkImageSubresourceRange":                ImageSubresourceRange{},
		"VkImageSubresourceLayers":               ImageSubresourceLayers{},
		"VkImageMemoryBarrier2":                  ImageMemoryBarrier2{},
		"VkDependencyInfo":                       DependencyInfo{},
		"VkBufferImageCopy":                      BufferImageCopy{},
		"VkShaderModuleCreateInfo":               ShaderModuleCreateInfo{},
		"VkOffset2D":                             Offset2D{},
		"VkExtent2D":                             Extent2D{},
		"VkRect2D":                               Rect2D{},
		"VkViewport":                             Viewport{},
		"VkComponentMapping":                     ComponentMapping{},
		"VkImageViewCreateInfo":                  ImageViewCreateInfo{},
		"VkPipelineLayoutCreateInfo":             PipelineLayoutCreateInfo{},
		"VkPipelineShaderStageCreateInfo":        PipelineShaderStageCreateInfo{},
		"VkPipelineVertexInputStateCreateInfo":   PipelineVertexInputStateCreateInfo{},
		"VkVertexInputBindingDescription":        VertexInputBindingDescription{},
		"VkDescriptorSetLayoutBinding":           DescriptorSetLayoutBinding{},
		"VkDescriptorSetLayoutCreateInfo":        DescriptorSetLayoutCreateInfo{},
		"VkDescriptorPoolSize":                   DescriptorPoolSize{},
		"VkDescriptorPoolCreateInfo":             DescriptorPoolCreateInfo{},
		"VkDescriptorSetAllocateInfo":            DescriptorSetAllocateInfo{},
		"VkDescriptorBufferInfo":                 DescriptorBufferInfo{},
		"VkWriteDescriptorSet":                   WriteDescriptorSet{},
		"VkSamplerCreateInfo":                    SamplerCreateInfo{},
		"VkDescriptorImageInfo":                  DescriptorImageInfo{},
		"VkVertexInputAttributeDescription":      VertexInputAttributeDescription{},
		"VkPipelineInputAssemblyStateCreateInfo": PipelineInputAssemblyStateCreateInfo{},
		"VkPipelineViewportStateCreateInfo":      PipelineViewportStateCreateInfo{},
		"VkPipelineRasterizationStateCreateInfo": PipelineRasterizationStateCreateInfo{},
		"VkPipelineMultisampleStateCreateInfo":   PipelineMultisampleStateCreateInfo{},
		"VkPipelineColorBlendAttachmentState":    PipelineColorBlendAttachmentState{},
		"VkPipelineColorBlendStateCreateInfo":    PipelineColorBlendStateCreateInfo{},
		"VkPipelineDynamicStateCreateInfo":       PipelineDynamicStateCreateInfo{},
		"VkPipelineRenderingCreateInfo":          PipelineRenderingCreateInfo{},
		"VkGraphicsPipelineCreateInfo":           GraphicsPipelineCreateInfo{},
		"VkRenderingAttachmentInfo":              RenderingAttachmentInfo{},
		"VkRenderingInfo":                        RenderingInfo{},
		"VkDebugUtilsMessengerCreateInfoEXT":     DebugUtilsMessengerCreateInfoEXT{},
		"VkDebugUtilsMessengerCallbackDataEXT":   DebugUtilsMessengerCallbackDataEXT{},
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
		"VK_STRUCTURE_TYPE_APPLICATION_INFO":                          StructureTypeApplicationInfo,
		"VK_STRUCTURE_TYPE_INSTANCE_CREATE_INFO":                      StructureTypeInstanceCreateInfo,
		"VK_STRUCTURE_TYPE_DEVICE_QUEUE_CREATE_INFO":                  StructureTypeDeviceQueueCreateInfo,
		"VK_STRUCTURE_TYPE_DEVICE_CREATE_INFO":                        StructureTypeDeviceCreateInfo,
		"VK_STRUCTURE_TYPE_PHYSICAL_DEVICE_VULKAN_1_3_FEATURES":       StructureTypePhysicalDeviceVulkan13Features,
		"VK_STRUCTURE_TYPE_PHYSICAL_DEVICE_FEATURES_2":                StructureTypePhysicalDeviceFeatures2,
		"VK_STRUCTURE_TYPE_MEMORY_ALLOCATE_INFO":                      StructureTypeMemoryAllocateInfo,
		"VK_STRUCTURE_TYPE_BUFFER_CREATE_INFO":                        StructureTypeBufferCreateInfo,
		"VK_STRUCTURE_TYPE_SUBMIT_INFO":                               StructureTypeSubmitInfo,
		"VK_STRUCTURE_TYPE_COMMAND_POOL_CREATE_INFO":                  StructureTypeCommandPoolCreateInfo,
		"VK_STRUCTURE_TYPE_COMMAND_BUFFER_ALLOCATE_INFO":              StructureTypeCommandBufferAllocateInfo,
		"VK_STRUCTURE_TYPE_COMMAND_BUFFER_BEGIN_INFO":                 StructureTypeCommandBufferBeginInfo,
		"VK_STRUCTURE_TYPE_IMAGE_CREATE_INFO":                         StructureTypeImageCreateInfo,
		"VK_STRUCTURE_TYPE_IMAGE_MEMORY_BARRIER_2":                    StructureTypeImageMemoryBarrier2,
		"VK_STRUCTURE_TYPE_DEPENDENCY_INFO":                           StructureTypeDependencyInfo,
		"VK_STRUCTURE_TYPE_SHADER_MODULE_CREATE_INFO":                 StructureTypeShaderModuleCreateInfo,
		"VK_STRUCTURE_TYPE_IMAGE_VIEW_CREATE_INFO":                    StructureTypeImageViewCreateInfo,
		"VK_STRUCTURE_TYPE_PIPELINE_SHADER_STAGE_CREATE_INFO":         StructureTypePipelineShaderStageCreateInfo,
		"VK_STRUCTURE_TYPE_PIPELINE_VERTEX_INPUT_STATE_CREATE_INFO":   StructureTypePipelineVertexInputStateCreateInfo,
		"VK_STRUCTURE_TYPE_DESCRIPTOR_SET_LAYOUT_CREATE_INFO":         StructureTypeDescriptorSetLayoutCreateInfo,
		"VK_STRUCTURE_TYPE_DESCRIPTOR_POOL_CREATE_INFO":               StructureTypeDescriptorPoolCreateInfo,
		"VK_STRUCTURE_TYPE_DESCRIPTOR_SET_ALLOCATE_INFO":              StructureTypeDescriptorSetAllocateInfo,
		"VK_STRUCTURE_TYPE_WRITE_DESCRIPTOR_SET":                      StructureTypeWriteDescriptorSet,
		"VK_STRUCTURE_TYPE_SAMPLER_CREATE_INFO":                       StructureTypeSamplerCreateInfo,
		"VK_STRUCTURE_TYPE_PIPELINE_INPUT_ASSEMBLY_STATE_CREATE_INFO": StructureTypePipelineInputAssemblyStateCreateInfo,
		"VK_STRUCTURE_TYPE_PIPELINE_VIEWPORT_STATE_CREATE_INFO":       StructureTypePipelineViewportStateCreateInfo,
		"VK_STRUCTURE_TYPE_PIPELINE_RASTERIZATION_STATE_CREATE_INFO":  StructureTypePipelineRasterizationStateCreateInfo,
		"VK_STRUCTURE_TYPE_PIPELINE_MULTISAMPLE_STATE_CREATE_INFO":    StructureTypePipelineMultisampleStateCreateInfo,
		"VK_STRUCTURE_TYPE_PIPELINE_COLOR_BLEND_STATE_CREATE_INFO":    StructureTypePipelineColorBlendStateCreateInfo,
		"VK_STRUCTURE_TYPE_PIPELINE_DYNAMIC_STATE_CREATE_INFO":        StructureTypePipelineDynamicStateCreateInfo,
		"VK_STRUCTURE_TYPE_GRAPHICS_PIPELINE_CREATE_INFO":             StructureTypeGraphicsPipelineCreateInfo,
		"VK_STRUCTURE_TYPE_PIPELINE_LAYOUT_CREATE_INFO":               StructureTypePipelineLayoutCreateInfo,
		"VK_STRUCTURE_TYPE_RENDERING_INFO":                            StructureTypeRenderingInfo,
		"VK_STRUCTURE_TYPE_RENDERING_ATTACHMENT_INFO":                 StructureTypeRenderingAttachmentInfo,
		"VK_STRUCTURE_TYPE_PIPELINE_RENDERING_CREATE_INFO":            StructureTypePipelineRenderingCreateInfo,
		"VK_STRUCTURE_TYPE_DEBUG_UTILS_MESSENGER_CREATE_INFO_EXT":     StructureTypeDebugUtilsMessengerCreateInfoEXT,
		"VK_STRUCTURE_TYPE_DEBUG_UTILS_MESSENGER_CALLBACK_DATA_EXT":   StructureTypeDebugUtilsMessengerCallbackDataEXT,
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

// The enum and flag values written here by hand, against the header
// ("NAME = 37," or "NAME = 0x00001000ULL;").
func TestConstantsMatchHeader(t *testing.T) {
	h := vulkanHeader(t)
	for c, v := range map[string]uint64{
		"VK_FORMAT_R8G8B8A8_UNORM":                        uint64(FormatR8G8B8A8Unorm),
		"VK_FORMAT_R32G32_SFLOAT":                         uint64(FormatR32G32Sfloat),
		"VK_FORMAT_R32G32B32A32_SFLOAT":                   uint64(FormatR32G32B32A32Sfloat),
		"VK_VERTEX_INPUT_RATE_VERTEX":                     uint64(VertexInputRateVertex),
		"VK_INDEX_TYPE_UINT32":                            uint64(IndexTypeUint32),
		"VK_DESCRIPTOR_TYPE_UNIFORM_BUFFER_DYNAMIC":       uint64(DescriptorTypeUniformBufferDynamic),
		"VK_DESCRIPTOR_TYPE_COMBINED_IMAGE_SAMPLER":       uint64(DescriptorTypeCombinedImageSampler),
		"VK_FILTER_NEAREST":                               uint64(FilterNearest),
		"VK_SAMPLER_MIPMAP_MODE_NEAREST":                  uint64(SamplerMipmapModeNearest),
		"VK_SAMPLER_ADDRESS_MODE_CLAMP_TO_BORDER":         uint64(SamplerAddressModeClampToBorder),
		"VK_BORDER_COLOR_FLOAT_TRANSPARENT_BLACK":         uint64(BorderColorFloatTransparentBlack),
		"VK_IMAGE_LAYOUT_SHADER_READ_ONLY_OPTIMAL":        uint64(ImageLayoutShaderReadOnlyOptimal),
		"VK_PIPELINE_STAGE_2_FRAGMENT_SHADER_BIT":         PipelineStage2FragmentShader,
		"VK_ACCESS_2_SHADER_SAMPLED_READ_BIT":             Access2ShaderSampledRead,
		"VK_DEBUG_UTILS_MESSAGE_SEVERITY_WARNING_BIT_EXT": DebugUtilsMessageSeverityWarningBitEXT,
		"VK_DEBUG_UTILS_MESSAGE_SEVERITY_ERROR_BIT_EXT":   DebugUtilsMessageSeverityErrorBitEXT,
		"VK_DEBUG_UTILS_MESSAGE_TYPE_VALIDATION_BIT_EXT":  DebugUtilsMessageTypeValidationBitEXT,
		"VK_DEBUG_UTILS_MESSAGE_TYPE_GENERAL_BIT_EXT":     DebugUtilsMessageTypeGeneralBitEXT,
		"VK_IMAGE_LAYOUT_COLOR_ATTACHMENT_OPTIMAL":        uint64(ImageLayoutColorAttachmentOptimal),
		"VK_IMAGE_VIEW_TYPE_2D":                           uint64(ImageViewType2D),
		"VK_PRIMITIVE_TOPOLOGY_TRIANGLE_LIST":             uint64(PrimitiveTopologyTriangleList),
		"VK_DYNAMIC_STATE_VIEWPORT":                       uint64(DynamicStateViewport),
		"VK_DYNAMIC_STATE_SCISSOR":                        uint64(DynamicStateScissor),
		"VK_ATTACHMENT_LOAD_OP_CLEAR":                     uint64(AttachmentLoadOpClear),
		"VK_ATTACHMENT_LOAD_OP_LOAD":                      uint64(AttachmentLoadOpLoad),
		"VK_ATTACHMENT_STORE_OP_STORE":                    uint64(AttachmentStoreOpStore),
		"VK_SHADER_STAGE_VERTEX_BIT":                      uint64(ShaderStageVertexBit),
		"VK_SHADER_STAGE_FRAGMENT_BIT":                    uint64(ShaderStageFragmentBit),
		"VK_SAMPLE_COUNT_1_BIT":                           uint64(SampleCount1Bit),
		"VK_PIPELINE_BIND_POINT_GRAPHICS":                 uint64(PipelineBindPointGraphics),
		"VK_PIPELINE_STAGE_2_COLOR_ATTACHMENT_OUTPUT_BIT": PipelineStage2ColorAttachmentOutput,
		"VK_ACCESS_2_COLOR_ATTACHMENT_WRITE_BIT":          Access2ColorAttachmentWrite,
		"VK_IMAGE_LAYOUT_UNDEFINED":                       uint64(ImageLayoutUndefined),
		"VK_IMAGE_LAYOUT_GENERAL":                         uint64(ImageLayoutGeneral),
		"VK_IMAGE_LAYOUT_TRANSFER_SRC_OPTIMAL":            uint64(ImageLayoutTransferSrcOptimal),
		"VK_IMAGE_LAYOUT_TRANSFER_DST_OPTIMAL":            uint64(ImageLayoutTransferDstOptimal),
		"VK_IMAGE_TYPE_2D":                                uint64(ImageType2D),
		"VK_IMAGE_USAGE_TRANSFER_SRC_BIT":                 uint64(ImageUsageTransferSrcBit),
		"VK_IMAGE_USAGE_TRANSFER_DST_BIT":                 uint64(ImageUsageTransferDstBit),
		"VK_IMAGE_USAGE_SAMPLED_BIT":                      uint64(ImageUsageSampledBit),
		"VK_IMAGE_USAGE_COLOR_ATTACHMENT_BIT":             uint64(ImageUsageColorAttachmentBit),
		"VK_IMAGE_ASPECT_COLOR_BIT":                       uint64(ImageAspectColorBit),
		"VK_PIPELINE_STAGE_2_ALL_TRANSFER_BIT":            PipelineStage2AllTransfer,
		"VK_PIPELINE_STAGE_2_HOST_BIT":                    PipelineStage2Host,
		"VK_ACCESS_2_TRANSFER_READ_BIT":                   Access2TransferRead,
		"VK_ACCESS_2_TRANSFER_WRITE_BIT":                  Access2TransferWrite,
		"VK_ACCESS_2_HOST_READ_BIT":                       Access2HostRead,
		"VK_MEMORY_PROPERTY_DEVICE_LOCAL_BIT":             uint64(MemoryPropertyDeviceLocalBit),
		"VK_MEMORY_PROPERTY_HOST_VISIBLE_BIT":             uint64(MemoryPropertyHostVisibleBit),
		"VK_MEMORY_PROPERTY_HOST_COHERENT_BIT":            uint64(MemoryPropertyHostCoherentBit),
		"VK_BUFFER_USAGE_TRANSFER_SRC_BIT":                uint64(BufferUsageTransferSrcBit),
		"VK_BUFFER_USAGE_TRANSFER_DST_BIT":                uint64(BufferUsageTransferDstBit),
		"VK_BUFFER_USAGE_UNIFORM_BUFFER_BIT":              uint64(BufferUsageUniformBufferBit),
		"VK_BUFFER_USAGE_INDEX_BUFFER_BIT":                uint64(BufferUsageIndexBufferBit),
		"VK_BUFFER_USAGE_VERTEX_BUFFER_BIT":               uint64(BufferUsageVertexBufferBit),
		"VK_QUEUE_GRAPHICS_BIT":                           uint64(QueueGraphicsBit),
		"VK_COMMAND_BUFFER_USAGE_ONE_TIME_SUBMIT_BIT":     uint64(CommandBufferUsageOneTimeSubmitBit),
		"VK_COMMAND_POOL_CREATE_RESET_COMMAND_BUFFER_BIT": uint64(CommandPoolCreateResetCommandBufferBit),
	} {
		m := regexp.MustCompile(`\b` + c + ` = (0x[0-9A-Fa-f]+|\d+)(?:ULL)?[,;]`).FindStringSubmatch(h)
		if m == nil {
			t.Errorf("%s: not in the header", c)
			continue
		}
		got, err := strconv.ParseUint(m[1], 0, 64)
		if err != nil || got != v {
			t.Errorf("%s = %s in the header, %#x here", c, m[1], v)
		}
	}
}
