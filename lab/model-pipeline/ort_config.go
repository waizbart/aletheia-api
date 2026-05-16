package main

import (
	"fmt"
	"os"
	"strings"

	ort "github.com/yalue/onnxruntime_go"
)

// buildSessionOptions configura ONNX Runtime: CUDA, ROCm (via AppendExecutionProvider),
// ou CPU. ORT_EP: auto (padrão), cpu, cuda, rocm.
func buildSessionOptions() (*ort.SessionOptions, error) {
	ep := strings.ToLower(strings.TrimSpace(os.Getenv("ORT_EP")))
	if ep == "" {
		ep = "auto"
	}

	switch ep {
	case "cpu":
		return ort.NewSessionOptions()
	case "cuda":
		opts, err := ort.NewSessionOptions()
		if err != nil {
			return nil, err
		}
		if err := appendCUDAProvider(opts); err != nil {
			opts.Destroy()
			return nil, err
		}
		return opts, nil
	case "rocm":
		opts, err := ort.NewSessionOptions()
		if err != nil {
			return nil, err
		}
		if err := opts.AppendExecutionProvider("ROCM", map[string]string{"device_id": "0"}); err != nil {
			opts.Destroy()
			return nil, fmt.Errorf("ROCM EP: %w", err)
		}
		return opts, nil
	case "auto":
		if opts, err := ort.NewSessionOptions(); err == nil {
			if appendCUDAProvider(opts) == nil {
				return opts, nil
			}
			opts.Destroy()
		}
		if opts, err := ort.NewSessionOptions(); err == nil {
			if err := opts.AppendExecutionProvider("ROCM", map[string]string{"device_id": "0"}); err == nil {
				return opts, nil
			}
			opts.Destroy()
		}
		return ort.NewSessionOptions()
	default:
		return nil, fmt.Errorf("ORT_EP inválido %q (use auto, cpu, cuda, rocm)", ep)
	}
}

func appendCUDAProvider(opts *ort.SessionOptions) error {
	co, err := ort.NewCUDAProviderOptions()
	if err != nil {
		return err
	}
	defer co.Destroy()
	if err := co.Update(map[string]string{"device_id": "0"}); err != nil {
		return err
	}
	return opts.AppendExecutionProviderCUDA(co)
}
