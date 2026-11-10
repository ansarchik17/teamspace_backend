package service

import "strings"

var visionModels = map[string]struct{}{
	"google/gemma-3-27b":               {},
	"Qwen/Qwen3-VL-235B-A22B-Instruct": {},
	"deepseek-ai/DeepSeek-OCR":         {},
}

// isModelMultimodal checks if a model supports vision
func isModelMultimodal(modelName string) bool {
	_, ok := visionModels[strings.TrimSpace(modelName)]
	return ok
}
