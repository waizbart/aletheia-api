"""
Export DINOv2 ViT-S/14 (728px) and RotNet to ONNX format.

Run once before building the Docker image:

    pip install torch transformers onnx tensorflow tf2onnx
    python export_models.py
    python quantize_models.py   # opcional — INT8

Output:
    models/dinov2_small.onnx
    models/rotnet_street_view.onnx
    models/rotnet_io.json       # nomes de I/O para a pipeline Go
"""

import os
import warnings

import torch
from transformers import AutoModel

warnings.filterwarnings("ignore")


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
    """Export DINOv2 ViT-S/14 (728px, 52x52 patches of 14px)."""
    dino = AutoModel.from_pretrained("facebook/dinov2-small")
    dino.eval()
    dummy = torch.randn(1, 3, 728, 728)
    common_kw = dict(
        f="models/dinov2_small.onnx",
        input_names=["pixel_values"],
        output_names=["last_hidden_state"],
        dynamic_axes={
            "pixel_values": {0: "batch"},
            "last_hidden_state": {0: "batch"},
        },
        opset_version=17,
    )
    # PyTorch recente: exportador Dynamo exige onnxscript; legado evita dependência extra.
    try:
        torch.onnx.export(dino, dummy, **common_kw, dynamo=False)
    except TypeError:
        torch.onnx.export(dino, dummy, **common_kw)
    _embed_external_data("models/dinov2_small.onnx")
    print("DINOv2-S (728px) exported -> models/dinov2_small.onnx")


def export_rotnet():
    """
    Export RotNet (ResNet50 + Dense 360) to ONNX.
    Input: NHWC [1, 224, 224, 3], normalized via imagenet_utils.preprocess_input
    Output: [1, 360] softmax probabilities over rotation angles 0..359
    """
    # Tentar importar tensorflow (pode ser tensorflow_cpu em alguns ambientes)
    try:
        import tensorflow as tf
    except ImportError:
        import tensorflow_cpu as tf
    import tf2onnx

    # Silenciar logs do TF
    tf.get_logger().setLevel("ERROR")

    model_paths = [
        "models/rotnet_street_view_resnet50_keras2.hdf5",
        "models/rotnet_street_view_resnet50.hdf5",
    ]

    model_path = None
    for p in model_paths:
        if os.path.exists(p):
            model_path = p
            break

    if model_path is None:
        print("ERRO: Nenhum modelo RotNet HDF5 encontrado em models/")
        print("Baixe de: https://github.com/d4nst/RotNet (pre-trained models)")
        return

    print(f"Carregando RotNet de: {model_path}")

    # Carregar modelo Keras
    model = tf.keras.models.load_model(model_path, compile=False)
    print(f"  Input : {model.inputs[0].shape}")
    print(f"  Output: {model.outputs[0].shape}")

    # Converter para ONNX
    spec = [tf.TensorSpec((None, 224, 224, 3), tf.float32, name="input")]
    output_path = "models/rotnet_street_view.onnx"

    model_proto, _ = tf2onnx.convert.from_keras(
        model,
        input_signature=spec,
        opset=17,
        output_path=output_path,
    )

    print(f"RotNet exported -> {output_path}")

    # Verificar nomes dos tensores de entrada e saída
    import json

    import onnx

    onnx_model = onnx.load(output_path)
    in_name = onnx_model.graph.input[0].name
    out_name = onnx_model.graph.output[0].name
    for inp in onnx_model.graph.input:
        print(f"  ONNX input : {inp.name} {inp.type.tensor_type.shape}")
    for out in onnx_model.graph.output:
        print(f"  ONNX output: {out.name} {out.type.tensor_type.shape}")

    manifest = {"input": in_name, "output": out_name}
    man_path = "models/rotnet_io.json"
    with open(man_path, "w", encoding="utf-8") as f:
        json.dump(manifest, f, indent=2)
    print(f"  manifest -> {man_path}")


if __name__ == "__main__":
    export_dinov2()
    export_rotnet()
    print("All models exported successfully.")
