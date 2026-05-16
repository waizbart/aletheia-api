#!/usr/bin/env python3
"""
Quantização dinâmica INT8 (pesos) para ONNX Runtime — reduz CPU e memória.

Requer: pip install onnx onnxruntime

Uso (após export_models.py):
    python quantize_models.py
"""

import os

import onnx
from onnxruntime.quantization import QuantType, quantize_dynamic


def quantize_if_exists(src: str, dst: str) -> bool:
    if not os.path.exists(src):
        print(f"Pular (não existe): {src}")
        return False
    print(f"Quantizando {src} -> {dst} ...")
    # onnxruntime>=1.19 removeu o parâmetro optimize_model de quantize_dynamic.
    quantize_dynamic(
        model_input=src,
        model_output=dst,
        weight_type=QuantType.QInt8,
    )
    # sanity check
    onnx.load(dst)
    print(f"  OK: {dst}")
    return True


def main() -> None:
    os.chdir(os.path.dirname(os.path.abspath(__file__)))
    quantize_if_exists("models/dinov2_small.onnx", "models/dinov2_small.int8.onnx")
    quantize_if_exists("models/rotnet_street_view.onnx", "models/rotnet_street_view.int8.onnx")


if __name__ == "__main__":
    main()
