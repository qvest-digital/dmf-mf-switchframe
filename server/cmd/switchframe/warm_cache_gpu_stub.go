//go:build !cgo || !cuda || !tensorrt

package main

func warmTensorRT(modelsDir string)      {}
func warmNeuralKeyPlan(modelPath string) {}
