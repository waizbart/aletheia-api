"""
Export DINOv2 ViT-S/14 and ConvNeXt V2-Base to ONNX format.

Run once before building the Docker image:

    pip install torch transformers timm onnx
    python export_models.py

Output:
    models/dinov2_small.onnx
    models/convnextv2_base.onnx
"""

import os
import torch
from transformers import AutoModel
import timm


def _embed_external_data(model_path: str):
    """Carrega um modelo .onnx com dados externos (.onnx.data)
    e salva novamente com todos os pesos embutidos no protobuf."""
    import onnx

    # Verificar se existe arquivo de dados externo
    data_path = model_path + ".data"
    if not os.path.exists(data_path):
        print(f"  (sem dados externos para {model_path})")
        return

    # Carregar modelo (incluindo dados externos)
    model = onnx.load(model_path, load_external_data=True)
    # Salvar com dados embutidos
    onnx.save(model, model_path)
    # Remover o arquivo de dados externo
    os.remove(data_path)
    print(f"  dados externos embutidos em {model_path}")


def export_dinov2():
    """Export DINOv2 ViT-S/14 (518px resolution, 37x37 patches of 14px)."""
    dino = AutoModel.from_pretrained("facebook/dinov2-small")
    dino.eval()
    dummy = torch.randn(1, 3, 518, 518)
    torch.onnx.export(
        dino,
        dummy,
        "models/dinov2_small.onnx",
        input_names=["pixel_values"],
        output_names=["last_hidden_state"],
        dynamic_axes={"pixel_values": {0: "batch"}},
        opset_version=17,
    )
    _embed_external_data("models/dinov2_small.onnx")
    print("DINOv2-S exported -> models/dinov2_small.onnx")


def export_convnext():
    """Export ConvNeXt V2-Base sem GAP — mapa espacial [1, 1024, H/32, W/32]."""
    convnext = timm.create_model(
        "convnextv2_base.fcmae_ft_in22k_in1k", pretrained=True
    )
    convnext.eval()

    # Substituir forward para capturar o mapa espacial (antes do GAP)
    # forward_features() = stem + stages + norm, sem pool
    convnext.forward = convnext.forward_features

    dummy = torch.randn(1, 3, 1088, 1088)
    torch.onnx.export(
        convnext,
        dummy,
        "models/convnextv2_base.onnx",
        input_names=["input"],
        output_names=["features"],
        dynamic_axes={
            "input": {0: "batch", 2: "height", 3: "width"},
            "features": {0: "batch", 2: "grid_h", 3: "grid_w"},
        },
        opset_version=17,
    )
    _embed_external_data("models/convnextv2_base.onnx")
    print("ConvNeXt V2-Base (spatial, no GAP) exported -> models/convnextv2_base.onnx")


if __name__ == "__main__":
    export_dinov2()
    export_convnext()
    print("All models exported successfully.")
